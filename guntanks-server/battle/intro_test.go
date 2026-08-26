package battle

import (
	"errors"
	"guntanks-server/engine"
	"testing"
	"time"
)

func TestIntroRejectsBattleCommands(t *testing.T) {
	a := NewActor(engine.NewState("b", 2, 1), 60)
	a.Configure(engine.NewTerrain(1200, 650), 30*time.Second)
	a.Start()
	defer a.Stop()
	for _, cmd := range []Command{
		{Type: "fire", TankID: "tank_1", Power: 50},
		{Type: "select_weapon", TankID: "tank_1", Weapon: engine.Shell2},
		{Type: "move_start", TankID: "tank_1", Direction: "right"},
		{Type: "aim_start", TankID: "tank_1", Direction: "up"},
	} {
		cmd.Reply = make(chan Event, 1)
		a.Commands <- cmd
		select {
		case ev := <-cmd.Reply:
			if !errors.Is(ev.Error, engine.ErrIntroInProgress) {
				t.Fatalf("command %s error=%v, want intro in progress", cmd.Type, ev.Error)
			}
		case <-time.After(time.Second):
			t.Fatalf("command %s timed out", cmd.Type)
		}
	}
}

func TestIntroDoesNotTimeoutBeforeEnd(t *testing.T) {
	a := NewActor(engine.NewState("b", 2, 1), 60)
	a.Configure(nil, 30*time.Second)
	a.State.TurnDeadlineMS = time.Now().Add(-time.Second).UnixMilli()
	a.tick()
	if a.State.Phase != "playing" || a.State.CurrentTankID != "tank_1" || a.State.TurnIndex != 0 {
		t.Fatalf("intro period should freeze turn: %+v", a.State)
	}
}

func TestIntroCompleteLandsTanksAndStartsTimer(t *testing.T) {
	a := NewActor(engine.NewState("b", 2, 1), 60)
	terrain := engine.NewTerrain(1200, 650)
	for y := 420; y < terrain.Height; y++ {
		for x := 0; x < terrain.Width; x++ {
			terrain.Solid[terrain.Index(x, y)] = true
		}
	}
	a.Configure(terrain, 30*time.Second)
	if a.State.IntroEndMS == 0 {
		t.Fatal("intro end not configured")
	}
	a.State.IntroEndMS = time.Now().Add(-10 * time.Millisecond).UnixMilli()
	a.State.TurnDeadlineMS = a.State.IntroEndMS + 30_000
	a.tick()
	for _, tank := range a.State.Tanks {
		if tank.Y != tank.LandY {
			t.Fatalf("tank %s did not land: y=%v land_y=%v", tank.ID, tank.Y, tank.LandY)
		}
	}
	if a.State.Phase != "playing" || a.State.TurnDeadlineMS != a.State.IntroEndMS+30_000 {
		t.Fatalf("unexpected post-intro state: %+v", a.State)
	}
	select {
	case ev := <-a.Events:
		if ev.Type != "battle.intro_complete" || ev.State.Phase != "playing" {
			t.Fatalf("unexpected event: %+v", ev)
		}
	default:
		t.Fatal("intro_complete event not emitted")
	}
}

func TestIntroLeaveEndsBattle(t *testing.T) {
	a := NewActor(engine.NewState("b", 2, 1), 60)
	a.Configure(nil, 30*time.Second)
	a.Start()
	defer a.Stop()
	reply := make(chan Event, 1)
	a.Commands <- Command{Type: "leave", TankID: "tank_1", Reply: reply}
	select {
	case ev := <-reply:
		if ev.Error != nil {
			t.Fatal(ev.Error)
		}
		if ev.State.Phase != "finished" || ev.State.WinnerTankID != "tank_2" {
			t.Fatalf("unexpected state: %+v", ev.State)
		}
	case <-time.After(time.Second):
		t.Fatal("leave timed out")
	}
	select {
	case ev := <-a.Events:
		if ev.Type != "battle.player_eliminated" || ev.State.Phase != "finished" {
			t.Fatalf("unexpected event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("elimination event not emitted")
	}
}
