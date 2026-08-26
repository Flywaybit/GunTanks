package battle

import (
	"context"
	"errors"
	"guntanks-server/dao"
	"guntanks-server/engine"
	"guntanks-server/protocol"
	"testing"
	"time"
)

func waitEvent(t *testing.T, ch <-chan protocol.Event, timeout time.Duration) protocol.Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(timeout):
		t.Fatal("timed out waiting for event")
		return protocol.Event{}
	}
}

func TestPauseRejectsBattleCommands(t *testing.T) {
	a := NewActor(engine.NewState("b", 2, 1), 60)
	pauseReply := make(chan Event, 1)
	a.apply(Command{Type: "pause", Reply: pauseReply})
	if ev := <-pauseReply; ev.Error != nil || !ev.State.Paused {
		t.Fatal(ev.Error)
	}
	for _, cmd := range []Command{
		{Type: "fire", TankID: "tank_1", Power: 50},
		{Type: "select_weapon", TankID: "tank_1", Weapon: engine.Shell2},
		{Type: "move_start", TankID: "tank_1", Direction: "right"},
		{Type: "aim_start", TankID: "tank_1", Direction: "up"},
	} {
		cmd.Reply = make(chan Event, 1)
		a.apply(cmd)
		select {
		case ev := <-cmd.Reply:
			if !errors.Is(ev.Error, engine.ErrBattlePaused) {
				t.Fatalf("command %s error=%v, want battle paused", cmd.Type, ev.Error)
			}
			if !ev.State.Paused {
				t.Fatalf("command %s state missing paused flag", cmd.Type)
			}
		case <-time.After(time.Second):
			t.Fatalf("command %s timed out", cmd.Type)
		}
	}
	// move_stop/aim_stop are lifecycle cleanup and stay available.
	stopReply := make(chan Event, 1)
	a.apply(Command{Type: "move_stop", TankID: "tank_1", Reply: stopReply})
	if ev := <-stopReply; ev.Error != nil {
		t.Fatalf("move_stop during pause: %v", ev.Error)
	}
}

func TestPauseFreezesTimeoutAndResumeExtendsDeadline(t *testing.T) {
	a := NewActor(engine.NewState("b", 2, 1), 60)
	base := time.Now().Add(5 * time.Second).UnixMilli()
	a.State.TurnDeadlineMS = base
	pauseReply := make(chan Event, 1)
	a.apply(Command{Type: "pause", Reply: pauseReply})
	if ev := <-pauseReply; ev.Error != nil || !ev.State.Paused {
		t.Fatalf("pause failed: err=%v state=%+v", ev.Error, ev.State)
	}
	time.Sleep(1100 * time.Millisecond)
	a.State.TurnDeadlineMS = time.Now().Add(-time.Second).UnixMilli()
	a.tick()
	if a.State.CurrentTankID != "tank_1" || a.State.Phase != "playing" || a.State.TurnsCompleted != 0 {
		t.Fatalf("paused actor timed out: %+v", a.State)
	}
	a.State.TurnDeadlineMS = base
	resumeReply := make(chan Event, 1)
	a.apply(Command{Type: "resume", Reply: resumeReply})
	ev := <-resumeReply
	if ev.Error != nil || ev.State.Paused {
		t.Fatalf("resume failed: err=%v state=%+v", ev.Error, ev.State)
	}
	if ev.State.TurnDeadlineMS < base+1000 {
		t.Fatalf("deadline not extended by pause duration: base=%d deadline=%d", base, ev.State.TurnDeadlineMS)
	}
}

func TestPauseDuringIntroAllowsResumeAndCompletesIntro(t *testing.T) {
	a := NewActor(engine.NewState("b", 2, 1), 60)
	a.Configure(nil, 30*time.Second)
	pauseReply := make(chan Event, 1)
	a.apply(Command{Type: "pause", Reply: pauseReply})
	if ev := <-pauseReply; ev.Error != nil {
		t.Fatal(ev.Error)
	}
	resumeReply := make(chan Event, 1)
	a.apply(Command{Type: "resume", Reply: resumeReply})
	if ev := <-resumeReply; ev.Error != nil || ev.State.Paused {
		t.Fatalf("resume during intro failed: err=%v state=%+v", ev.Error, ev.State)
	}
	a.State.IntroEndMS = time.Now().Add(-10 * time.Millisecond).UnixMilli()
	a.State.TurnDeadlineMS = a.State.IntroEndMS + 30_000
	a.tick()
	deadline := time.After(time.Second)
	for {
		select {
		case ev := <-a.Events:
			if ev.Type != "battle.intro_complete" {
				continue
			}
			for _, tank := range ev.State.Tanks {
				if tank.Y != tank.LandY {
					t.Fatalf("intro did not complete after resume: %+v", ev.State.Tanks)
				}
			}
			return
		case <-deadline:
			t.Fatal("intro_complete not emitted after resume")
		}
	}
}

func TestDisconnectPausesAndReconnectResumes(t *testing.T) {
	manager := NewManager(dao.NewMemoryStore(), 60, 5*time.Second)
	defer manager.Shutdown(context.Background())
	events1, close1 := manager.Subscribe("u1")
	defer close1()
	events2, close2 := manager.Subscribe("u2")
	state, _, err := manager.Create(context.Background(), "matchmaking", []Player{{UserID: "u1", Username: "one"}, {UserID: "u2", Username: "two"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	waitEvent(t, events1, time.Second)
	waitEvent(t, events2, time.Second)
	close2()
	if !waitForPaused(t, events1, 3*time.Second) {
		t.Fatal("u1 did not receive paused=true after u2 disconnected")
	}
	_, close3 := manager.Subscribe("u2")
	defer close3()
	if !waitForUnpaused(t, events1, 3*time.Second) {
		t.Fatal("u1 did not receive paused=false after u2 reconnected")
	}
	_ = state
}

func waitForPaused(t *testing.T, ch <-chan protocol.Event, timeout time.Duration) bool {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-ch:
			payload, _ := ev.Payload.(engine.State)
			if payload.Paused {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

func waitForUnpaused(t *testing.T, ch <-chan protocol.Event, timeout time.Duration) bool {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-ch:
			payload, _ := ev.Payload.(engine.State)
			if !payload.Paused {
				return true
			}
		case <-deadline:
			return false
		}
	}
}
