package web

import (
	"context"
	"testing"
	"time"

	"guntanks-server/battle"
	"guntanks-server/dao"
	gredis "guntanks-server/redis"
)

func TestFinalizeConnectionReleasesOnlineFromLobby(t *testing.T) {
	p := gredis.NewMemoryPresence()
	ctx := context.Background()
	if ok, err := p.AcquireOnline(ctx, "u1", "s1", time.Minute); err != nil || !ok {
		t.Fatalf("acquire online: ok=%v err=%v", ok, err)
	}
	if err := p.SetReconnect(ctx, "u1", "battle_1", time.Minute); err != nil {
		t.Fatal(err)
	}
	gw := &Gateway{
		Presence:       p,
		Battles:        battle.NewManager(dao.NewMemoryStore(), 60, time.Second),
		ReconnectGrace: time.Second,
		owners:         map[string]uint64{"u1\x00s1": 1},
	}
	defer gw.Battles.Shutdown(context.Background())

	gw.finalizeConnection(ctx, &managedConn{id: 1, userID: "u1", sessionID: "s1"}, false)

	if ok, _ := p.RefreshOnline(ctx, "u1", "s1", time.Minute); ok {
		t.Fatal("online lease still present")
	}
	if _, ok, _ := p.ReconnectBattle(ctx, "u1"); ok {
		t.Fatal("reconnect lease still present")
	}
}

func TestFinalizeConnectionKeepsOnlineDuringBattleReconnect(t *testing.T) {
	p := gredis.NewMemoryPresence()
	ctx := context.Background()
	if ok, err := p.AcquireOnline(ctx, "u1", "s1", time.Minute); err != nil || !ok {
		t.Fatalf("acquire online: ok=%v err=%v", ok, err)
	}
	manager := battle.NewManager(dao.NewMemoryStore(), 60, time.Second)
	defer manager.Shutdown(context.Background())
	if _, _, err := manager.Create(ctx, "matchmaking", []battle.Player{
		{UserID: "u1", Username: "one"},
		{UserID: "u2", Username: "two"},
	}, 1); err != nil {
		t.Fatal(err)
	}
	gw := &Gateway{
		Presence:       p,
		Battles:        manager,
		ReconnectGrace: time.Second,
		owners:         map[string]uint64{"u1\x00s1": 1},
	}

	gw.finalizeConnection(ctx, &managedConn{id: 1, userID: "u1", sessionID: "s1"}, false)

	if ok, _ := p.RefreshOnline(ctx, "u1", "s1", time.Minute); !ok {
		t.Fatal("online lease was released")
	}
	if battleID, ok, _ := p.ReconnectBattle(ctx, "u1"); !ok || battleID == "" {
		t.Fatal("reconnect lease missing")
	}
}

func TestFinalizeConnectionReleasesOnlineAfterBattleLeave(t *testing.T) {
	p := gredis.NewMemoryPresence()
	ctx := context.Background()
	if ok, err := p.AcquireOnline(ctx, "u1", "s1", time.Minute); err != nil || !ok {
		t.Fatalf("acquire online: ok=%v err=%v", ok, err)
	}
	if err := p.SetReconnect(ctx, "u1", "battle_1", time.Minute); err != nil {
		t.Fatal(err)
	}
	manager := battle.NewManager(dao.NewMemoryStore(), 60, time.Second)
	defer manager.Shutdown(context.Background())
	if _, _, err := manager.Create(ctx, "matchmaking", []battle.Player{
		{UserID: "u1", Username: "one"},
		{UserID: "u2", Username: "two"},
	}, 1); err != nil {
		t.Fatal(err)
	}
	gw := &Gateway{
		Presence:       p,
		Battles:        manager,
		ReconnectGrace: time.Second,
		owners:         map[string]uint64{"u1\x00s1": 1},
	}

	gw.finalizeConnection(ctx, &managedConn{id: 1, userID: "u1", sessionID: "s1"}, true)

	if ok, _ := p.RefreshOnline(ctx, "u1", "s1", time.Minute); ok {
		t.Fatal("online lease still present")
	}
	if _, ok, _ := p.ReconnectBattle(ctx, "u1"); ok {
		t.Fatal("reconnect lease still present")
	}
}

func TestFinalizeConnectionSkipsStaleConnection(t *testing.T) {
	p := gredis.NewMemoryPresence()
	ctx := context.Background()
	if ok, err := p.AcquireOnline(ctx, "u1", "s1", time.Minute); err != nil || !ok {
		t.Fatalf("acquire online: ok=%v err=%v", ok, err)
	}
	gw := &Gateway{
		Presence:       p,
		Battles:        battle.NewManager(dao.NewMemoryStore(), 60, time.Second),
		ReconnectGrace: time.Second,
		owners:         map[string]uint64{"u1\x00s1": 2},
	}
	defer gw.Battles.Shutdown(context.Background())

	gw.finalizeConnection(ctx, &managedConn{id: 1, userID: "u1", sessionID: "s1"}, false)

	if ok, _ := p.RefreshOnline(ctx, "u1", "s1", time.Minute); !ok {
		t.Fatal("stale connection released the online lease")
	}
}
