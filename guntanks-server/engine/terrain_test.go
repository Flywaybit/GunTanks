package engine

import "testing"

func TestTerrainSnapshotChecksum(t *testing.T) {
	m := NewTerrain(32, 32)
	for i := range m.Solid {
		m.Solid[i] = true
	}
	m.DestroyCircle(16, 16, 4)
	s, e := m.Snapshot(1)
	if e != nil {
		t.Fatal(e)
	}
	ok, e := VerifySnapshot(s)
	if e != nil || !ok {
		t.Fatalf("snapshot verification failed: %v", e)
	}
}
