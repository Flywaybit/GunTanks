package engine

import "testing"

func TestGravityEliminatesOutOfBoundsTank(t *testing.T) {
	s := NewState("b", 2, 1)
	tank, _ := s.Tank("tank_1")
	tank.Y = 651
	changed, eliminated := s.ApplyGravityAndEliminate(nil)
	if !changed || len(eliminated) != 1 || eliminated[0] != "tank_1" || tank.Alive || tank.Health != 0 {
		t.Fatalf("unexpected elimination: changed=%v ids=%v tank=%+v", changed, eliminated, tank)
	}
	if s.Phase != "finished" || s.WinnerTankID != "tank_2" {
		t.Fatalf("unexpected result: %+v", s)
	}
}

func TestGravityBatchDrawForSimultaneousFalls(t *testing.T) {
	s := NewState("b", 2, 1)
	s.Tanks[0].Y, s.Tanks[1].Y = 651, 651
	changed, eliminated := s.ApplyGravityAndEliminate(nil)
	if !changed || len(eliminated) != 2 || s.Phase != "finished" || s.Result != "draw" || s.Revision != 1 || s.EventSeq != 1 {
		t.Fatalf("unexpected batch result: changed=%v ids=%v state=%+v", changed, eliminated, s)
	}
}

func TestGravityKeepsCurrentTurnWhenOtherTankFalls(t *testing.T) {
	s := NewState("b", 2, 1)
	s.Tanks[1].Y = 651
	s.ApplyGravityAndEliminate(nil)
	if s.CurrentTankID != "tank_1" || s.Phase != "finished" || s.WinnerTankID != "tank_1" {
		t.Fatalf("unexpected non-current fall state: %+v", s)
	}
}

func TestMoveLimitAndAimBounds(t *testing.T) {
	s := NewState("b", 2, 1)
	if err := s.Move("tank_1", "right", 81); err == nil {
		t.Fatal("expected move limit")
	}
	for i := 0; i < 60; i++ {
		_ = s.Aim("tank_1", "up")
	}
	tank, _ := s.Tank("tank_1")
	if tank.Angle != 120 {
		t.Fatalf("angle=%v", tank.Angle)
	}
}
func TestFireAdvancesTurn(t *testing.T) {
	s := NewState("b", 2, 1)
	shot, err := s.Fire("tank_1", 50)
	if err != nil {
		t.Fatal(err)
	}
	if shot.Weapon != Shell1 || s.CurrentTankID != "tank_2" || s.EventSeq != 1 {
		t.Fatalf("unexpected state: %+v", s)
	}
}

func TestSSCooldownMatchesBrowserSemantics(t *testing.T) {
	s := NewState("b", 2, 1)
	if err := s.SelectWeapon("tank_1", SS); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Fire("tank_1", 50); err != nil {
		t.Fatal(err)
	}
	tank, _ := s.Tank("tank_1")
	if tank.SSCooldown != 2 || tank.Weapon != Shell1 {
		t.Fatalf("cooldown=%d weapon=%s", tank.SSCooldown, tank.Weapon)
	}
}

func TestWindChangesEveryThreeRounds(t *testing.T) {
	s := NewState("b", 2, 7)
	initial := s.Wind
	for i := 0; i < 6; i++ {
		s.TimeoutTurn()
	}
	if s.Round != 4 || s.WindChanges != 1 {
		t.Fatalf("round=%d changes=%d", s.Round, s.WindChanges)
	}
	if s.Wind == initial {
		t.Fatal("wind did not change")
	}
}

func TestTrajectoryIsBoundedAndTerrainDestroyed(t *testing.T) {
	s := NewState("b", 2, 3)
	terrain := NewTerrain(1200, 650)
	for y := 420; y < 650; y++ {
		for x := 0; x < 1200; x++ {
			terrain.Solid[terrain.Index(x, y)] = true
		}
	}
	shot, err := s.FireWithTerrain("tank_1", 70, terrain)
	if err != nil {
		t.Fatal(err)
	}
	if len(shot.Trajectory) == 0 || len(shot.Trajectory) > 240 {
		t.Fatalf("trajectory points=%d", len(shot.Trajectory))
	}
	if shot.TerrainDestroyed.Radius != 48 {
		t.Fatalf("radius=%d", shot.TerrainDestroyed.Radius)
	}
}

func TestApplyIntroCompleteLandsTanksAndBumpsSequence(t *testing.T) {
	s := NewState("b", 2, 1)
	s.Tanks[0].LandY, s.Tanks[0].Y = 420, -200
	s.Tanks[1].LandY, s.Tanks[1].Y = 300, -200
	beforeRev, beforeSeq := s.Revision, s.EventSeq
	eliminated := s.ApplyIntroComplete(nil)
	if len(eliminated) != 0 {
		t.Fatalf("unexpected elimination: %v", eliminated)
	}
	if s.Tanks[0].Y != 420 || s.Tanks[1].Y != 300 {
		t.Fatalf("tanks did not land: %+v", s.Tanks)
	}
	if s.Revision != beforeRev+1 || s.EventSeq != beforeSeq+1 {
		t.Fatalf("revision/event seq not bumped: rev=%d seq=%d", s.Revision, s.EventSeq)
	}
}

func TestApplyIntroCompleteEliminatesOutOfBoundsLanding(t *testing.T) {
	s := NewState("b", 2, 1)
	s.Tanks[0].LandY, s.Tanks[0].Y = 700, -200
	s.Tanks[1].LandY, s.Tanks[1].Y = 300, -200
	eliminated := s.ApplyIntroComplete(nil)
	if len(eliminated) != 1 || eliminated[0] != "tank_1" {
		t.Fatalf("unexpected elimination: %v", eliminated)
	}
	if s.Tanks[0].Alive || s.Tanks[0].Health != 0 {
		t.Fatalf("tank_1 should be eliminated: %+v", s.Tanks[0])
	}
	if s.Phase != "finished" || s.WinnerTankID != "tank_2" {
		t.Fatalf("unexpected result: %+v", s)
	}
}
