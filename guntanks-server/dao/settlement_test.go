package dao

import (
	"context"
	"testing"
)

func TestMemorySettlementIsIdempotent(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	_ = store.CreateUser(ctx, UserRecord{UserID: "u1", Username: "one"})
	_ = store.CreateUser(ctx, UserRecord{UserID: "u2", Username: "two"})
	battle := BattleRecord{BattleID: "b1", Players: []BattlePlayer{{UserID: "u1", Result: "win"}, {UserID: "u2", Result: "loss"}}}
	_ = store.SaveBattle(ctx, battle)
	if changed, err := store.SettleBattle(ctx, battle); err != nil || !changed {
		t.Fatalf("first settlement: changed=%v err=%v", changed, err)
	}
	if changed, err := store.SettleBattle(ctx, battle); err != nil || changed {
		t.Fatalf("second settlement: changed=%v err=%v", changed, err)
	}
	winner, _ := store.GetUserByID(ctx, "u1")
	loser, _ := store.GetUserByID(ctx, "u2")
	if winner.Wins != 1 || winner.GamesPlayed != 1 || loser.Losses != 1 || loser.GamesPlayed != 1 {
		t.Fatalf("winner=%+v loser=%+v", winner, loser)
	}
}
