package battle

import (
	"context"
	"guntanks-server/dao"
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
