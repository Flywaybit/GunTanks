package battle

import (
	"guntanks-server/engine"
	"testing"
	"time"
)

func TestActorProcessesCommands(t *testing.T) {
	a := NewActor(engine.NewState("b", 2, 1), 60)
	a.Start()
	defer a.Stop()
	a.Commands <- Command{Type: "aim_start", TankID: "tank_1", Direction: "up"}
	select {
	case e := <-a.Events:
		if e.Error != nil {
			t.Fatal(e.Error)
		}
	case <-time.After(time.Second):
		t.Fatal("actor timeout")
	}
}

func TestPausedActorDoesNotResolveFallUntilResumed(t *testing.T) {
	a := NewActor(engine.NewState("b", 2, 1), 60)
	a.State.Tanks[0].Y = 651
	a.paused = true
	a.tick()
	if !a.State.Tanks[0].Alive || a.State.Phase == "finished" {
		t.Fatalf("paused actor changed battle state: %+v", a.State)
	}
	a.paused = false
	a.tick()
	if a.State.Tanks[0].Alive || a.State.Phase != "finished" {
		t.Fatalf("resumed actor did not resolve fall: %+v", a.State)
	}
}
