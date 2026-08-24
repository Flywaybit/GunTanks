package redis

import (
	"context"
	"testing"
	"time"
)

func TestMemoryPresenceGenerationOwnership(t *testing.T) {
	p := NewMemoryPresence()
	ctx := context.Background()
	if ok, _ := p.AcquireOnline(ctx, "u", "one", time.Minute); !ok {
		t.Fatal("first lease rejected")
	}
	if ok, _ := p.AcquireOnline(ctx, "u", "two", time.Minute); ok {
		t.Fatal("duplicate lease accepted")
	}
	if ok, _ := p.RefreshOnline(ctx, "u", "two", time.Minute); ok {
		t.Fatal("wrong generation refreshed")
	}
	_ = p.ReleaseOnline(ctx, "u", "two")
	if ok, _ := p.RefreshOnline(ctx, "u", "one", time.Minute); !ok {
		t.Fatal("owner lease removed")
	}
}
