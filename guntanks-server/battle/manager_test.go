package battle

import (
	"context"
	"guntanks-server/dao"
	"guntanks-server/engine"
	"guntanks-server/protocol"
	"testing"
	"time"
)

func TestPreconnectedPlayersReceiveBattleStarted(t *testing.T) {
	manager := NewManager(dao.NewMemoryStore(), 60, time.Second)
	defer manager.Shutdown(context.Background())
	events1, close1 := manager.Subscribe("u1")
	defer close1()
	events2, close2 := manager.Subscribe("u2")
	defer close2()
	state, _, err := manager.Create(context.Background(), "matchmaking", []Player{{UserID: "u1", Username: "one"}, {UserID: "u2", Username: "two"}}, 4)
	if err != nil {
		t.Fatal(err)
	}
	for index, events := range []<-chan protocol.Event{events1, events2} {
		select {
		case event := <-events:
			if event.Type != "battle.started" || event.BattleID != state.BattleID {
				t.Fatalf("client %d event=%+v", index, event)
			}
		case <-time.After(time.Second):
			t.Fatalf("client %d timed out", index)
		}
	}
}

func TestLeaveIgnoresStaleRevision(t *testing.T) {
	manager := NewManager(dao.NewMemoryStore(), 60, time.Second)
	defer manager.Shutdown(context.Background())
	events, closeConn := manager.Subscribe("u1")
	defer closeConn()
	state, _, err := manager.Create(context.Background(), "matchmaking", []Player{{UserID: "u1", Username: "one"}, {UserID: "u2", Username: "two"}}, 7)
	if err != nil { t.Fatal(err) }
	<-events
	if err := manager.Submit(context.Background(), "u1", protocol.Message{Type: "battle.leave", BattleID: state.BattleID, Revision: 1, RequestID: "leave-1"}); err != nil { t.Fatal(err) }
}

func TestStartedCarriesIntroFields(t *testing.T) {
	manager := NewManager(dao.NewMemoryStore(), 60, time.Second)
	defer manager.Shutdown(context.Background())
	events, closeConn := manager.Subscribe("u1")
	defer closeConn()
	manager.Subscribe("u2")
	state, _, err := manager.Create(context.Background(), "matchmaking", []Player{{UserID: "u1", Username: "one"}, {UserID: "u2", Username: "two"}}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if state.IntroEndMS == 0 {
		t.Fatal("intro_end_ms missing from returned state")
	}
	for _, tank := range state.Tanks {
		if tank.Y != -200 || tank.LandY == 0 {
			t.Fatalf("intro tank state missing: %+v", tank)
		}
	}
	if state.TurnDeadlineMS != state.IntroEndMS+30_000 {
		t.Fatalf("turn deadline should start after intro: deadline=%d intro_end=%d", state.TurnDeadlineMS, state.IntroEndMS)
	}
	select {
	case event := <-events:
		if event.Type != "battle.started" {
			t.Fatalf("unexpected event: %+v", event)
		}
		payload, _ := event.Payload.(map[string]any)
		statePayload, _ := payload["state"].(engine.State)
		if statePayload.IntroEndMS == 0 {
			t.Fatal("battle.started state missing intro_end_ms")
		}
		if statePayload.Tanks[0].Y != -200 || statePayload.Tanks[0].LandY == 0 {
			t.Fatalf("battle.started tank missing intro fields: %+v", statePayload.Tanks[0])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for battle.started")
	}
}

func TestSubmitRejectsCommandsDuringIntro(t *testing.T) {
	manager := NewManager(dao.NewMemoryStore(), 60, time.Second)
	defer manager.Shutdown(context.Background())
	events, closeConn := manager.Subscribe("u1")
	defer closeConn()
	manager.Subscribe("u2")
	state, _, err := manager.Create(context.Background(), "matchmaking", []Player{{UserID: "u1", Username: "one"}, {UserID: "u2", Username: "two"}}, 4)
	if err != nil {
		t.Fatal(err)
	}
	<-events // battle.started
	if err := manager.Submit(context.Background(), "u1", protocol.Message{Type: "battle.fire", BattleID: state.BattleID, Revision: 0, RequestID: "fire-1", Payload: map[string]any{"power": 50.0}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(time.Second)
	for {
		select {
		case event := <-events:
			if event.Type != "error" {
				continue
			}
			payload, _ := event.Payload.(map[string]any)
			if payload["code"] != "INTRO_IN_PROGRESS" {
				t.Fatalf("unexpected error code: %v", payload["code"])
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for rejection")
		}
	}
}

func TestIntroCompletesAndBroadcasts(t *testing.T) {
	manager := NewManager(dao.NewMemoryStore(), 60, time.Second)
	defer manager.Shutdown(context.Background())
	events1, close1 := manager.Subscribe("u1")
	defer close1()
	events2, close2 := manager.Subscribe("u2")
	defer close2()
	state, _, err := manager.Create(context.Background(), "matchmaking", []Player{{UserID: "u1", Username: "one"}, {UserID: "u2", Username: "two"}}, 4)
	if err != nil {
		t.Fatal(err)
	}
	<-events1
	<-events2
	for index, events := range []<-chan protocol.Event{events1, events2} {
		select {
		case event := <-events:
			if event.Type != "battle.intro_complete" {
				t.Fatalf("client %d event=%+v", index, event)
			}
			payload, _ := event.Payload.(engine.State)
			if payload.Phase != "playing" || payload.BattleID != state.BattleID {
				t.Fatalf("client %d intro state=%+v", index, payload)
			}
			for _, tank := range payload.Tanks {
				if tank.Y != tank.LandY {
					t.Fatalf("client %d tank did not land: %+v", index, tank)
				}
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("client %d timed out waiting for intro_complete", index)
		}
	}
}

func TestSnapshotPlayersAreArrays(t *testing.T) {
	manager := NewManager(dao.NewMemoryStore(), 60, time.Second)
	defer manager.Shutdown(context.Background())
	events1, close1 := manager.Subscribe("u1")
	defer close1()
	manager.Subscribe("u2")
	state, _, err := manager.Create(context.Background(), "matchmaking", []Player{{UserID: "u1", Username: "one"}, {UserID: "u2", Username: "two"}}, 4)
	if err != nil {
		t.Fatal(err)
	}
	waitEvent(t, events1, time.Second)

	// battle.resync path (line 337 equivalent).
	if err := manager.Submit(context.Background(), "u1", protocol.Message{Type: "battle.resync", BattleID: state.BattleID, RequestID: "resync-1"}); err != nil {
		t.Fatal(err)
	}
	event := waitEvent(t, events1, time.Second)
	if event.Type != "battle.snapshot" {
		t.Fatalf("unexpected event: %+v", event)
	}
	payload, _ := event.Payload.(map[string]any)
	players, ok := payload["players"].([]Player)
	if !ok || len(players) != 2 {
		t.Fatalf("snapshot players is not a 2-element array: %T %+v", payload["players"], payload["players"])
	}
	byTank := map[string]Player{}
	for _, p := range players {
		if p.Username == "" || p.TankID == "" {
			t.Fatalf("snapshot player missing username/tank_id: %+v", p)
		}
		byTank[p.TankID] = p
	}
	if byTank["tank_1"].Username != "one" || byTank["tank_2"].Username != "two" {
		t.Fatalf("snapshot players mapping wrong: %+v", byTank)
	}

	// Reconnect subscribe path (line 272 equivalent).
	close1()
	events1b, close1b := manager.Subscribe("u1")
	defer close1b()
	for {
		event := waitEvent(t, events1b, 2*time.Second)
		if event.Type != "battle.snapshot" {
			continue
		}
		payload, _ := event.Payload.(map[string]any)
		players, ok := payload["players"].([]Player)
		if !ok || len(players) != 2 {
			t.Fatalf("reconnect snapshot players is not an array: %T %+v", payload["players"], payload["players"])
		}
		break
	}
}

func TestFinishedCarriesPlayersArray(t *testing.T) {
	manager := NewManager(dao.NewMemoryStore(), 60, time.Second)
	defer manager.Shutdown(context.Background())
	events1, close1 := manager.Subscribe("u1")
	defer close1()
	events2, close2 := manager.Subscribe("u2")
	defer close2()
	state, _, err := manager.Create(context.Background(), "matchmaking", []Player{{UserID: "u1", Username: "one"}, {UserID: "u2", Username: "two"}}, 4)
	if err != nil {
		t.Fatal(err)
	}
	waitEvent(t, events1, time.Second)
	waitEvent(t, events2, time.Second)
	if err := manager.Submit(context.Background(), "u1", protocol.Message{Type: "battle.leave", BattleID: state.BattleID, RequestID: "leave-1"}); err != nil {
		t.Fatal(err)
	}
	event := waitEvent(t, events1, 3*time.Second)
	if event.Type != "battle.finished" {
		t.Fatalf("unexpected event: %+v", event)
	}
	payload, ok := event.Payload.(map[string]any)
	if !ok {
		t.Fatalf("finished payload is not a wrapper object: %T", event.Payload)
	}
	finishedState, ok := payload["state"].(engine.State)
	if !ok || finishedState.Phase != "finished" || finishedState.WinnerTankID != "tank_2" {
		t.Fatalf("finished wrapper state wrong: %+v", payload["state"])
	}
	players, ok := payload["players"].([]Player)
	if !ok || len(players) != 2 {
		t.Fatalf("finished players is not a 2-element array: %T %+v", payload["players"], payload["players"])
	}
	for _, p := range players {
		if p.Username == "" || p.TankID == "" {
			t.Fatalf("finished player missing username/tank_id: %+v", p)
		}
	}
}

func TestFinishedAfterKillingShotCarriesPlayers(t *testing.T) {
	manager := NewManager(dao.NewMemoryStore(), 60, time.Second)
	defer manager.Shutdown(context.Background())
	events1, close1 := manager.Subscribe("u1")
	defer close1()
	events2, close2 := manager.Subscribe("u2")
	defer close2()
	state, _, err := manager.Create(context.Background(), "matchmaking", []Player{{UserID: "u1", Username: "one"}, {UserID: "u2", Username: "two"}}, 4)
	if err != nil {
		t.Fatal(err)
	}
	waitEvent(t, events1, time.Second)
	waitEvent(t, events2, time.Second)
	for {
		ev := waitEvent(t, events1, 3*time.Second)
		if ev.Type == "battle.intro_complete" {
			break
		}
	}
	rt := manager.battles[state.BattleID]
	rt.actor.State.Wind.Speed = 0
	rt.actor.State.Tanks[1].Health = 100
	if err := manager.Submit(context.Background(), "u1", protocol.Message{Type: "battle.fire", BattleID: state.BattleID, RequestID: "kill-1", Payload: map[string]any{"power": 80.0}}); err != nil {
		t.Fatal(err)
	}
	var finished protocol.Event
	for {
		ev := waitEvent(t, events1, 3*time.Second)
		if ev.Type == "battle.finished" {
			finished = ev
			break
		}
	}
	payload, ok := finished.Payload.(map[string]any)
	if !ok {
		t.Fatalf("final battle.finished payload is not a wrapper object: %T", finished.Payload)
	}
	finishedState, ok := payload["state"].(engine.State)
	if !ok || finishedState.Phase != "finished" || finishedState.WinnerTankID != "tank_1" {
		t.Fatalf("final finished wrapper state wrong: %+v", payload["state"])
	}
	players, ok := payload["players"].([]Player)
	if !ok || len(players) != 2 {
		t.Fatalf("final finished players is not a 2-element array: %T %+v", payload["players"], payload["players"])
	}
	for _, p := range players {
		if p.Username == "" || p.TankID == "" {
			t.Fatalf("final finished player missing username/tank_id: %+v", p)
		}
	}
}
