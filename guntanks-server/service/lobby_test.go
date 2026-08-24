package service

import "testing"

func TestLobbyQueueAndRoom(t *testing.T) {
	l := NewLobby()
	if _, ready, e := l.JoinQueue("a", 2); e != nil || ready {
		t.Fatal("first player should wait")
	}
	g, ready, e := l.JoinQueue("b", 2)
	if e != nil || !ready || len(g) != 2 {
		t.Fatal("match should complete")
	}
	r, e := l.CreateRoom("a", "test", 2)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = l.JoinRoom(r.ID, "b"); e != nil {
		t.Fatal(e)
	}
	if _, e = l.SetReady(r.ID, "a", true); e != nil {
		t.Fatal(e)
	}
	if _, e = l.SetReady(r.ID, "b", true); e != nil {
		t.Fatal(e)
	}
	r, e = l.StartRoom(r.ID, "a")
	if e != nil || r.Status != "playing" {
		t.Fatal("room should start")
	}
}

func TestRoomHostMigrationAndClose(t *testing.T) {
	l := NewLobby()
	r, err := l.CreateRoom("u1", "test", 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = l.JoinRoom(r.ID, "u2"); err != nil {
		t.Fatal(err)
	}
	r, closed, err := l.LeaveRoom(r.ID, "u1")
	if err != nil || closed || r.Host != "u2" {
		t.Fatalf("room=%+v closed=%v err=%v", r, closed, err)
	}
	_, closed, err = l.LeaveRoom(r.ID, "u2")
	if err != nil || !closed {
		t.Fatalf("closed=%v err=%v", closed, err)
	}
}

func TestQueueReservationRollbackAndCancel(t *testing.T) {
	l := NewLobby()
	if _, ready, err := l.JoinQueueRequest("a", "r-a", 2); err != nil || ready { t.Fatal(err, ready) }
	if users, ready, err := l.JoinQueueRequest("b", "r-b", 2); err != nil || !ready || len(users) != 2 { t.Fatal(users, ready, err) }
	l.RestoreQueue([]string{"a", "b"})
	l.CancelQueue("a")
	if _, ok := l.RequestForUser("a"); ok { t.Fatal("cancelled reservation remains") }
	if users, ready, err := l.JoinQueueRequest("a", "r-a2", 2); err != nil || !ready || len(users) != 2 { t.Fatal(users, ready, err) }
}
