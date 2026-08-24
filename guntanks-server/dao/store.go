package dao

import (
	"context"
	"errors"
	"guntanks-server/engine"
	"sync"
	"time"
)

var ErrNotFound = errors.New("not found")
var ErrDuplicate = errors.New("duplicate")

type UserRecord struct {
	UserID       string    `json:"user_id" bson:"user_id"`
	Username     string    `json:"username" bson:"username"`
	PasswordHash string    `json:"-" bson:"password_hash"`
	Wins         int       `json:"wins" bson:"wins"`
	Losses       int       `json:"losses" bson:"losses"`
	Draws        int       `json:"draws" bson:"draws"`
	GamesPlayed  int       `json:"games_played" bson:"games_played"`
	CreatedAt    time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt    time.Time `json:"updated_at" bson:"updated_at"`
	LastLoginAt  time.Time `json:"last_login_at,omitempty" bson:"last_login_at,omitempty"`
}

type BattlePlayer struct {
	UserID        string `json:"user_id" bson:"user_id"`
	Username      string `json:"username" bson:"username"`
	TankID        string `json:"tank_id" bson:"tank_id"`
	Result        string `json:"result,omitempty" bson:"result,omitempty"`
	EliminatedSeq uint64 `json:"eliminated_at_seq,omitempty" bson:"eliminated_at_seq,omitempty"`
}

type BattleRecord struct {
	BattleID  string         `json:"battle_id" bson:"battle_id"`
	Source    string         `json:"source" bson:"source"`
	Players   []BattlePlayer `json:"players" bson:"players"`
	State     engine.State   `json:"state" bson:"state"`
	Status    string         `json:"status" bson:"status"`
	CreatedAt time.Time      `json:"created_at" bson:"created_at"`
	UpdatedAt time.Time      `json:"updated_at" bson:"updated_at"`
	Settled   bool           `json:"settled" bson:"settled"`
}

type EventRecord struct {
	BattleID     string    `json:"battle_id" bson:"battle_id"`
	Seq          uint64    `json:"seq" bson:"seq"`
	Revision     uint64    `json:"revision" bson:"revision"`
	Type         string    `json:"type" bson:"type"`
	ActorUserID  string    `json:"actor_user_id,omitempty" bson:"actor_user_id,omitempty"`
	ServerTimeMS int64     `json:"server_time_ms" bson:"server_time_ms"`
	Payload      any       `json:"payload" bson:"payload"`
	CreatedAt    time.Time `json:"created_at" bson:"created_at"`
}

type TerrainSnapshotRecord struct {
	BattleID    string    `json:"battle_id" bson:"battle_id"`
	SnapshotSeq uint64    `json:"snapshot_seq" bson:"snapshot_seq"`
	Width       int       `json:"width" bson:"width"`
	Height      int       `json:"height" bson:"height"`
	Encoding    string    `json:"encoding" bson:"encoding"`
	Data        []byte    `json:"data,omitempty" bson:"data"`
	Checksum    string    `json:"checksum" bson:"checksum"`
	CreatedAt   time.Time `json:"created_at" bson:"created_at"`
}

type Store interface {
	SaveBattle(context.Context, BattleRecord) error
	CreateUser(context.Context, UserRecord) error
	GetUserByUsername(context.Context, string) (UserRecord, error)
	GetUserByID(context.Context, string) (UserRecord, error)
	TouchLogin(context.Context, string) error
	GetBattle(context.Context, string) (BattleRecord, error)
	ListBattlesForUser(context.Context, string, int) ([]BattleRecord, error)
	AppendEvent(context.Context, EventRecord) error
	ListEvents(context.Context, string, uint64) ([]EventRecord, error)
	SaveTerrainSnapshot(context.Context, TerrainSnapshotRecord) error
	GetLatestTerrainSnapshot(context.Context, string) (TerrainSnapshotRecord, error)
	SettleBattle(context.Context, BattleRecord) (bool, error)
}

type MemoryStore struct {
	mu      sync.RWMutex
	users   map[string]UserRecord
	userIDs map[string]string
	battles map[string]BattleRecord
	events  map[string][]EventRecord
	terrain map[string]TerrainSnapshotRecord
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{users: map[string]UserRecord{}, userIDs: map[string]string{}, battles: map[string]BattleRecord{}, events: map[string][]EventRecord{}, terrain: map[string]TerrainSnapshotRecord{}}
}

func (s *MemoryStore) CreateUser(_ context.Context, u UserRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[u.Username]; ok {
		return ErrDuplicate
	}
	s.users[u.Username] = u
	s.userIDs[u.UserID] = u.Username
	return nil
}

func (s *MemoryStore) GetUserByUsername(_ context.Context, username string) (UserRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.users[username]
	if !ok {
		return UserRecord{}, ErrNotFound
	}
	return u, nil
}

func (s *MemoryStore) GetUserByID(_ context.Context, id string) (UserRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	username, ok := s.userIDs[id]
	if !ok {
		return UserRecord{}, ErrNotFound
	}
	return s.users[username], nil
}

func (s *MemoryStore) TouchLogin(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	username, ok := s.userIDs[id]
	if !ok {
		return ErrNotFound
	}
	u := s.users[username]
	u.LastLoginAt = time.Now()
	u.UpdatedAt = time.Now()
	s.users[username] = u
	return nil
}

func (s *MemoryStore) SaveBattle(_ context.Context, b BattleRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.battles[b.BattleID] = b
	return nil
}

func (s *MemoryStore) SettleBattle(_ context.Context, b BattleRecord) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.battles[b.BattleID]
	if ok && current.Settled {
		return false, nil
	}
	b.Settled = true
	s.battles[b.BattleID] = b
	for _, player := range b.Players {
		username, exists := s.userIDs[player.UserID]
		if !exists {
			continue
		}
		u := s.users[username]
		u.GamesPlayed++
		switch player.Result {
		case "win":
			u.Wins++
		case "loss":
			u.Losses++
		case "draw":
			u.Draws++
		}
		u.UpdatedAt = time.Now()
		s.users[username] = u
	}
	return true, nil
}

func (s *MemoryStore) GetBattle(_ context.Context, id string) (BattleRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.battles[id]
	if !ok {
		return BattleRecord{}, context.Canceled
	}
	return b, nil
}

func (s *MemoryStore) ListBattlesForUser(_ context.Context, userID string, limit int) ([]BattleRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	out := make([]BattleRecord, 0, limit)
	for _, b := range s.battles {
		for _, p := range b.Players {
			if p.UserID == userID {
				out = append(out, b)
				break
			}
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *MemoryStore) AppendEvent(_ context.Context, e EventRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events[e.BattleID] = append(s.events[e.BattleID], e)
	return nil
}

func (s *MemoryStore) ListEvents(_ context.Context, id string, afterSeq uint64) ([]EventRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []EventRecord{}
	for _, e := range s.events[id] {
		if e.Seq > afterSeq {
			out = append(out, e)
		}
	}
	return out, nil
}

func (s *MemoryStore) SaveTerrainSnapshot(_ context.Context, snap TerrainSnapshotRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.terrain[snap.BattleID] = snap
	return nil
}

func (s *MemoryStore) GetLatestTerrainSnapshot(_ context.Context, battleID string) (TerrainSnapshotRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.terrain[battleID]
	if !ok {
		return TerrainSnapshotRecord{}, context.Canceled
	}
	return snap, nil
}
