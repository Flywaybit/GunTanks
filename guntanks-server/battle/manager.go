package battle

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"guntanks-server/dao"
	"guntanks-server/engine"
	"guntanks-server/protocol"
	"sync"
	"time"
)

var ErrBattleNotFound = errors.New("battle not found")
var ErrUserNotInBattle = errors.New("user not in battle")
var ErrManagerShuttingDown = errors.New("server shutting down")

type Player struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	TankID   string `json:"tank_id"`
}

type client struct {
	userID string
	send   chan protocol.Event
	once   sync.Once
	mu     sync.Mutex
	closed bool
}

func (c *client) close() {
	c.once.Do(func() {
		c.mu.Lock()
		c.closed = true
		close(c.send)
		c.mu.Unlock()
	})
}

func (c *client) trySend(ev protocol.Event) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return false
	}
	select {
	case c.send <- ev:
		return true
	default:
		return false
	}
}

type runtime struct {
	actor                  *Actor
	record                 dao.BattleRecord
	players                map[string]Player
	tanksByUser            map[string]string
	clients                map[string]*client
	state                  engine.State
	terrain                *engine.TerrainMask
	snapshotSeq            uint64
	destroyedSinceSnapshot int
	lastSnapshot           time.Time
	mu                     sync.RWMutex
}

type Manager struct {
	store                 dao.Store
	tickHz                int
	reconnectGrace        time.Duration
	turnTimeout           time.Duration
	terrainPath           string
	mapVersion            string
	snapshotEventInterval int
	snapshotInterval      time.Duration
	mu                    sync.RWMutex
	battles               map[string]*runtime
	connections           map[string]*client
	stopping              bool
	shutdownOnce          sync.Once
}

func NewManager(store dao.Store, tickHz int, reconnectGrace time.Duration) *Manager {
	if tickHz <= 0 {
		tickHz = 60
	}
	if reconnectGrace <= 0 {
		reconnectGrace = 60 * time.Second
	}
	return &Manager{store: store, tickHz: tickHz, reconnectGrace: reconnectGrace, turnTimeout: 30 * time.Second, snapshotEventInterval: 10, snapshotInterval: 30 * time.Second, battles: map[string]*runtime{}, connections: map[string]*client{}}
}
func (m *Manager) SetTurnTimeout(timeout time.Duration) {
	if timeout > 0 {
		m.turnTimeout = timeout
	}
}
func (m *Manager) SetTerrainPath(path string) { m.terrainPath = path }
func (m *Manager) SetSnapshotPolicy(events int, interval time.Duration) {
	if events > 0 {
		m.snapshotEventInterval = events
	}
	if interval > 0 {
		m.snapshotInterval = interval
	}
}

func (m *Manager) Create(ctx context.Context, source string, users []Player, seed int64) (engine.State, []Player, error) {
	m.mu.RLock()
	stopping := m.stopping
	m.mu.RUnlock()
	if stopping {
		return engine.State{}, nil, ErrManagerShuttingDown
	}
	if len(users) < 2 || len(users) > 4 {
		return engine.State{}, nil, errors.New("player_count must be 2..4")
	}
	id := fmt.Sprintf("battle_%d", time.Now().UnixNano())
	state := engine.NewState(id, len(users), seed)
	state.TurnDeadlineMS = time.Now().Add(m.turnTimeout).UnixMilli()
	players := make([]Player, len(users))
	recordPlayers := make([]dao.BattlePlayer, len(users))
	tanksByUser := map[string]string{}
	playersByUser := map[string]Player{}
	for i, u := range users {
		u.TankID = state.Tanks[i].ID
		players[i] = u
		tanksByUser[u.UserID] = u.TankID
		playersByUser[u.UserID] = u
		recordPlayers[i] = dao.BattlePlayer{UserID: u.UserID, Username: u.Username, TankID: u.TankID}
	}
	rec := dao.BattleRecord{BattleID: id, Source: source, Players: recordPlayers, State: state, Status: "ongoing", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	terrain, mapVersion, terrainErr := engine.LoadTerrainPNG(m.terrainPath, 1200, 650, 50, 320)
	if terrainErr != nil {
		terrain = engine.NewTerrain(1200, 650)
		for y := 420; y < terrain.Height; y++ {
			for x := 0; x < terrain.Width; x++ {
				terrain.Solid[terrain.Index(x, y)] = true
			}
		}
		mapVersion = "development-fallback"
	}
	m.mapVersion = mapVersion
	rt := &runtime{actor: NewActor(state, m.tickHz), record: rec, players: playersByUser, tanksByUser: tanksByUser, clients: map[string]*client{}, state: state, terrain: terrain, lastSnapshot: time.Now()}
	rt.actor.Configure(rt.terrain, m.turnTimeout)
	if snap, err := rt.terrain.Snapshot(0); err == nil {
		_ = m.store.SaveTerrainSnapshot(ctx, dao.TerrainSnapshotRecord{BattleID: id, SnapshotSeq: snap.SnapshotSeq, Width: snap.Width, Height: snap.Height, Encoding: snap.Encoding, Data: snap.Data, Checksum: snap.Checksum, CreatedAt: time.Now()})
	}
	if err := m.store.SaveBattle(ctx, rec); err != nil {
		return engine.State{}, nil, err
	}
	m.mu.Lock()
	m.battles[id] = rt
	for userID := range playersByUser {
		if c := m.connections[userID]; c != nil {
			rt.clients[userID] = c
		}
	}
	m.mu.Unlock()
	rt.actor.Start()
	go m.pump(rt)
	started := protocol.Event{Type: "battle.started", BattleID: id, Revision: state.Revision, EventSeq: state.EventSeq, Payload: map[string]any{"battle_id": id, "seed": seed, "server_time_ms": time.Now().UnixMilli(), "map_version": mapVersion, "players": players, "state": state, "terrain_snapshot": map[string]any{"snapshot_seq": 0, "href": "/api/v1/battles/" + id + "/terrain-snapshot"}}}
	m.append(ctx, rt, started, "")
	m.broadcast(rt, started)
	return state, players, nil
}

func (m *Manager) Snapshot(id string) (engine.State, bool) {
	rt := m.get(id)
	if rt == nil {
		return engine.State{}, false
	}
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.state, true
}

func (m *Manager) TankForUser(battleID, userID string) (string, bool) {
	rt := m.get(battleID)
	if rt == nil {
		return "", false
	}
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	tank, ok := rt.tanksByUser[userID]
	return tank, ok
}
func (m *Manager) BattleForUser(userID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for id, rt := range m.battles {
		if _, ok := rt.tanksByUser[userID]; ok {
			return id, true
		}
	}
	return "", false
}
func (m *Manager) SendUser(userID string, event protocol.Event) {
	m.mu.RLock()
	c := m.connections[userID]
	m.mu.RUnlock()
	if c != nil {
		c.trySend(event)
	}
}
func (m *Manager) Shutdown(ctx context.Context) error {
	var result error
	m.shutdownOnce.Do(func() {
		m.mu.Lock()
		m.stopping = true
		runtimes := make([]*runtime, 0, len(m.battles))
		for _, rt := range m.battles {
			runtimes = append(runtimes, rt)
		}
		m.battles = map[string]*runtime{}
		m.mu.Unlock()
		for _, rt := range runtimes {
			rt.actor.Stop()
			rt.mu.Lock()
			rt.record.Status = "interrupted"
			rt.record.UpdatedAt = time.Now()
			record := rt.record
			rt.mu.Unlock()
			if err := m.store.SaveBattle(ctx, record); err != nil && result == nil {
				result = err
			}
		}
	})
	return result
}

func (m *Manager) Subscribe(userID string) (<-chan protocol.Event, func()) {
	ch := make(chan protocol.Event, 64)
	c := &client{userID: userID, send: ch}
	m.mu.Lock()
	if old := m.connections[userID]; old != nil {
		old.close()
	}
	m.connections[userID] = c
	var rts []*runtime
	for _, rt := range m.battles {
		if _, ok := rt.tanksByUser[userID]; ok {
			rts = append(rts, rt)
		}
	}
	m.mu.Unlock()
	for _, rt := range rts {
		rt.mu.Lock()
		if old := rt.clients[userID]; old != nil {
			old.close()
		}
		rt.clients[userID] = c
		allConnected := true
		for id := range rt.tanksByUser {
			if rt.clients[id] == nil {
				allConnected = false
				break
			}
		}
		if allConnected {
			rt.actor.Commands <- Command{Type: "resume"}
		}
		state := rt.state
		rt.mu.Unlock()
		ch <- protocol.Event{Type: "reconnect.accepted", BattleID: state.BattleID, Revision: state.Revision, EventSeq: state.EventSeq, Payload: map[string]any{"battle_id": state.BattleID, "event_seq": state.EventSeq}}
		terrainSnapshot, _ := rt.terrain.Snapshot(rt.snapshotSeq)
		ch <- protocol.Event{Type: "battle.snapshot", BattleID: state.BattleID, Revision: state.Revision, EventSeq: state.EventSeq, Payload: map[string]any{"state": state, "players": rt.players, "terrain_snapshot": terrainSnapshot}}
	}
	return ch, func() {
		m.mu.Lock()
		if m.connections[userID] == c {
			delete(m.connections, userID)
		}
		rts = rts[:0]
		for _, rt := range m.battles {
			rts = append(rts, rt)
		}
		m.mu.Unlock()
		for _, rt := range rts {
			rt.mu.Lock()
			if rt.clients[userID] == c {
				delete(rt.clients, userID)
				rt.actor.Commands <- Command{Type: "pause"}
				tankID := rt.tanksByUser[userID]
				time.AfterFunc(m.reconnectGrace, func() {
					current := m.get(rt.state.BattleID)
					if current == nil {
						return
					}
					current.mu.RLock()
					_, connected := current.clients[userID]
					current.mu.RUnlock()
					if !connected {
						_ = m.Submit(context.Background(), userID, protocol.Message{Type: "battle.leave", BattleID: rt.state.BattleID})
						_ = tankID
					}
				})
			}
			rt.mu.Unlock()
		}
		c.close()
	}
}

func (m *Manager) Submit(ctx context.Context, userID string, msg protocol.Message) error {
	m.mu.RLock()
	stopping := m.stopping
	m.mu.RUnlock()
	if stopping {
		return ErrManagerShuttingDown
	}
	rt := m.get(msg.BattleID)
	if rt == nil {
		return ErrBattleNotFound
	}
	rt.mu.RLock()
	tankID, ok := rt.tanksByUser[userID]
	currentRevision := rt.state.Revision
	rt.mu.RUnlock()
	if !ok {
		return ErrUserNotInBattle
	}
	if msg.Type != "battle.leave" && msg.Revision != 0 && msg.Revision < currentRevision {
		m.sendTo(rt, userID, protocol.Event{Type: "error", BattleID: msg.BattleID, Revision: currentRevision, Payload: map[string]any{"code": "STALE_REVISION", "message": "stale revision", "request_id": msg.RequestID, "retryable": true}})
		return nil
	}
	if msg.Type == "battle.resync" {
		rt.mu.RLock()
		state := rt.state
		rt.mu.RUnlock()
		terrainSnapshot, _ := rt.terrain.Snapshot(rt.snapshotSeq)
		m.sendTo(rt, userID, protocol.Event{Type: "battle.snapshot", BattleID: state.BattleID, Revision: state.Revision, EventSeq: state.EventSeq, Payload: map[string]any{"state": state, "players": rt.players, "terrain_snapshot": terrainSnapshot}})
		after, _ := msg.Payload["last_event_seq"].(float64)
		if events, err := m.store.ListEvents(ctx, msg.BattleID, uint64(after)); err == nil {
			for _, e := range events {
				m.sendTo(rt, userID, protocol.Event{Type: e.Type, BattleID: e.BattleID, Revision: e.Revision, EventSeq: e.Seq, Payload: e.Payload})
			}
		}
		return nil
	}
	reply := make(chan Event, 1)
	cmd := Command{TankID: tankID, Reply: reply}
	switch msg.Type {
	case "battle.move_start":
		cmd.Type = "move_start"
		cmd.Direction, _ = msg.Payload["direction"].(string)
	case "battle.move_stop":
		cmd.Type = "move_stop"
	case "battle.aim_start":
		cmd.Type = "aim_start"
		cmd.Direction, _ = msg.Payload["direction"].(string)
	case "battle.aim_stop":
		cmd.Type = "aim_stop"
	case "battle.select_weapon":
		cmd.Type = "select_weapon"
		w, _ := msg.Payload["weapon"].(string)
		cmd.Weapon = engine.Weapon(w)
	case "battle.fire":
		cmd.Type = "fire"
		cmd.Power, _ = msg.Payload["power"].(float64)
	case "battle.leave":
		cmd.Type = "leave"
	default:
		return errors.New("unsupported message")
	}
	select {
	case rt.actor.Commands <- cmd:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case ev := <-reply:
		if ev.Error != nil {
			m.sendTo(rt, userID, protocol.Event{Type: "error", BattleID: msg.BattleID, Payload: map[string]any{"code": codeForError(ev.Error), "message": ev.Error.Error(), "request_id": msg.RequestID}})
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func (m *Manager) LatestTerrainSnapshot(ctx context.Context, battleID string) (map[string]any, error) {
	snap, err := m.store.GetLatestTerrainSnapshot(ctx, battleID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"battle_id": battleID, "snapshot_seq": snap.SnapshotSeq, "width": snap.Width, "height": snap.Height, "encoding": snap.Encoding, "checksum": snap.Checksum, "data_base64": base64.StdEncoding.EncodeToString(snap.Data)}, nil
}

func (m *Manager) get(id string) *runtime {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.battles[id]
}

func (m *Manager) pump(rt *runtime) {
	for ev := range rt.actor.Events {
		m.mu.RLock()
		stopping := m.stopping
		m.mu.RUnlock()
		if stopping {
			return
		}
		rt.mu.Lock()
		rt.state = ev.State
		rt.record.State = ev.State
		if ev.State.Phase == "finished" {
			rt.record.Status = ev.State.Result
			for i := range rt.record.Players {
				player := &rt.record.Players[i]
				if ev.State.Result == "draw" {
					player.Result = "draw"
				} else if player.TankID == ev.State.WinnerTankID {
					player.Result = "win"
				} else {
					player.Result = "loss"
				}
			}
		}
		for i := range rt.record.Players {
			if rt.record.Players[i].EliminatedSeq != 0 {
				continue
			}
			for _, tank := range ev.State.Tanks {
				if tank.ID == rt.record.Players[i].TankID && !tank.Alive {
					rt.record.Players[i].EliminatedSeq = ev.State.EventSeq
				}
			}
		}
		rt.record.UpdatedAt = time.Now()
		rt.mu.Unlock()
		out := protocol.Event{Type: ev.Type, BattleID: ev.State.BattleID, Revision: ev.State.Revision, EventSeq: ev.State.EventSeq, Payload: ev.State}
		if ev.Shot != nil {
			out.Type = "battle.shot_resolved"
			out.Payload = map[string]any{"shot": ev.Shot, "state": ev.State}
			rt.destroyedSinceSnapshot++
			if rt.destroyedSinceSnapshot >= m.snapshotEventInterval || time.Since(rt.lastSnapshot) >= m.snapshotInterval {
				if snap, err := rt.terrain.Snapshot(rt.snapshotSeq + 1); err == nil {
					rt.snapshotSeq++
					rt.destroyedSinceSnapshot = 0
					rt.lastSnapshot = time.Now()
					_ = m.store.SaveTerrainSnapshot(context.Background(), dao.TerrainSnapshotRecord{BattleID: ev.State.BattleID, SnapshotSeq: snap.SnapshotSeq, Width: snap.Width, Height: snap.Height, Encoding: snap.Encoding, Data: snap.Data, Checksum: snap.Checksum, CreatedAt: time.Now()})
				}
			}
		}
		if ev.State.Phase == "finished" && ev.Shot == nil {
			out.Type = "battle.finished"
			out.Payload = ev.State
		}
		m.broadcast(rt, out)
		m.append(context.Background(), rt, out, "")
		if ev.State.Phase == "finished" && ev.Shot != nil {
			final := protocol.Event{Type: "battle.finished", BattleID: ev.State.BattleID, Revision: ev.State.Revision, EventSeq: ev.State.EventSeq, Payload: ev.State}
			m.broadcast(rt, final)
			m.append(context.Background(), rt, final, "")
		}
		_ = m.store.SaveBattle(context.Background(), rt.record)
		if ev.State.Phase == "finished" {
			_, _ = m.store.SettleBattle(context.Background(), rt.record)
			rt.actor.Stop()
			m.mu.Lock()
			delete(m.battles, ev.State.BattleID)
			m.mu.Unlock()
			return
		}
	}
}

func (m *Manager) broadcast(rt *runtime, ev protocol.Event) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	for userID := range rt.tanksByUser {
		m.sendToLocked(rt, userID, ev)
	}
}

func (m *Manager) sendTo(rt *runtime, userID string, ev protocol.Event) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	m.sendToLocked(rt, userID, ev)
}

func (m *Manager) sendToLocked(rt *runtime, userID string, ev protocol.Event) {
	c := rt.clients[userID]
	if c == nil {
		return
	}
	c.trySend(ev)
}

func (m *Manager) append(ctx context.Context, rt *runtime, ev protocol.Event, actorUserID string) {
	if ev.EventSeq == 0 && ev.Type != "battle.started" {
		return
	}
	_ = m.store.AppendEvent(ctx, dao.EventRecord{BattleID: ev.BattleID, Seq: ev.EventSeq, Revision: ev.Revision, Type: ev.Type, ActorUserID: actorUserID, ServerTimeMS: time.Now().UnixMilli(), Payload: ev.Payload, CreatedAt: time.Now()})
}

func codeForError(err error) string {
	switch err.Error() {
	case "not your turn":
		return "NOT_YOUR_TURN"
	case "weapon cooldown":
		return "WEAPON_COOLDOWN"
	default:
		return "INVALID_ARGUMENT"
	}
}
