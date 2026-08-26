package web

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"guntanks-server/battle"
	"guntanks-server/engine"
	"guntanks-server/protocol"
	gredis "guntanks-server/redis"
	"guntanks-server/service"
)

type Gateway struct {
	Upgrader                  websocket.Upgrader
	Auth                      *service.Auth
	Battles                   *battle.Manager
	MaxMsgBytes               int64
	Presence                  gredis.Presence
	Lobby                     *service.Lobby
	OnlineTTL, ReconnectGrace time.Duration
	mu                        sync.Mutex
	connections               map[uint64]*managedConn
	nextID                    uint64
	shuttingDown              atomic.Bool
	wg                        sync.WaitGroup
	closeTimeout              time.Duration
	writeTimeout              time.Duration
	owners                    map[string]uint64
}

type managedConn struct {
	id        uint64
	userID    string
	sessionID string
	conn      *websocket.Conn
	done      chan struct{}
	outbound  chan protocol.Event
	once      sync.Once
}

func NewGateway(auth *service.Auth, battles *battle.Manager, lobby *service.Lobby, presence gredis.Presence, maxMsgBytes int, onlineTTL, reconnectGrace time.Duration) *Gateway {
	if maxMsgBytes <= 0 {
		maxMsgBytes = 262144
	}
	return &Gateway{
		Upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		Auth:     auth, Battles: battles, Lobby: lobby, Presence: presence, OnlineTTL: onlineTTL, ReconnectGrace: reconnectGrace, MaxMsgBytes: int64(maxMsgBytes), connections: map[uint64]*managedConn{}, owners: map[string]uint64{}, closeTimeout: 2 * time.Second, writeTimeout: time.Second,
	}
}
func (g *Gateway) SetShutdownTimeouts(closeTimeout, writeTimeout time.Duration) {
	if closeTimeout > 0 {
		g.closeTimeout = closeTimeout
	}
	if writeTimeout > 0 {
		g.writeTimeout = writeTimeout
	}
}

func (g *Gateway) IsShuttingDown() bool { return g.shuttingDown.Load() }
func (g *Gateway) BeginShutdown()       { g.shuttingDown.Store(true) }

// CloseAll performs the WebSocket close handshake, then forces blocked readers out
// once the shutdown deadline expires. It is safe to call more than once.
func (g *Gateway) CloseAll(ctx context.Context) error {
	g.BeginShutdown()
	waitCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		timeout := g.closeTimeout
		if timeout <= 0 {
			timeout = 2 * time.Second
		}
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	g.mu.Lock()
	connections := make([]*managedConn, 0, len(g.connections))
	for _, c := range g.connections {
		connections = append(connections, c)
	}
	g.mu.Unlock()
	deadline := time.Now().Add(g.writeTimeout)
	for _, c := range connections {
		_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down"), deadline)
	}
	done := make(chan struct{})
	go func() { g.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-waitCtx.Done():
		for _, c := range connections {
			c.conn.Close()
		}
		<-done
		return waitCtx.Err()
	}
}

func (g *Gateway) register(c *websocket.Conn, userID, sessionID string) *managedConn {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.shuttingDown.Load() {
		_ = c.Close()
		return nil
	}
	g.nextID++
	key := userID + "\x00" + sessionID
	if oldID := g.owners[key]; oldID != 0 {
		if old := g.connections[oldID]; old != nil {
			_ = old.conn.Close()
		}
	}
	m := &managedConn{id: g.nextID, userID: userID, sessionID: sessionID, conn: c, done: make(chan struct{})}
	g.connections[m.id] = m
	g.owners[key] = m.id
	g.wg.Add(1)
	return m
}
func (g *Gateway) emitUser(userID string, ev protocol.Event) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, c := range g.connections {
		if c.userID != userID || c.outbound == nil {
			continue
		}
		select {
		case c.outbound <- ev:
		default:
		}
	}
}
func (g *Gateway) setOutbound(m *managedConn, outbound chan protocol.Event) {
	g.mu.Lock()
	m.outbound = outbound
	g.mu.Unlock()
}
func (g *Gateway) unregister(m *managedConn) {
	m.once.Do(func() {
		g.mu.Lock()
		delete(g.connections, m.id)
		key := m.userID + "\x00" + m.sessionID
		if g.owners[key] == m.id {
			delete(g.owners, key)
		}
		g.mu.Unlock()
		close(m.done)
		g.wg.Done()
	})
}

func (g *Gateway) finalizeConnection(ctx context.Context, m *managedConn, intentionalLeave bool) {
	g.mu.Lock()
	currentOwner := g.owners[m.userID+"\x00"+m.sessionID] == m.id
	g.mu.Unlock()
	if !currentOwner {
		return
	}
	if !intentionalLeave {
		if battleID, ok := g.Battles.BattleForUser(m.userID); ok {
			if state, active := g.Battles.Snapshot(battleID); active && state.Phase == "playing" {
				_ = g.Presence.SetReconnect(ctx, m.userID, battleID, g.ReconnectGrace)
				return
			}
		}
	}
	_ = g.Presence.ReleaseOnline(ctx, m.userID, m.sessionID)
	_ = g.Presence.ClearReconnect(ctx, m.userID)
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if g.IsShuttingDown() {
		http.Error(w, "server shutting down", http.StatusServiceUnavailable)
		return
	}
	u, err := g.Auth.Verify(r.URL.Query().Get("token"))
	if err != nil {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}
	if ok, _ := g.Presence.RefreshOnline(r.Context(), u.ID, u.SessionID, g.OnlineTTL); !ok {
		w.Header().Set("X-GunTanks-Code", "SESSION_EXPIRED")
		http.Error(w, "session expired", http.StatusUnauthorized)
		return
	}
	c, err := g.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	m := g.register(c, u.ID, u.SessionID)
	if m == nil {
		return
	}
	intentionalLeave := false
	done := make(chan struct{})
	stop := make(chan struct{})
	outbound := make(chan protocol.Event, 128)
	var workers sync.WaitGroup
	workers.Add(2)
	g.setOutbound(m, outbound)
	defer c.Close()
	defer func() {
		close(stop)
		workers.Wait()
		g.finalizeConnection(context.Background(), m, intentionalLeave)
		g.unregister(m)
	}()
	c.SetReadLimit(g.MaxMsgBytes)
	events, unsubscribe := g.Battles.Subscribe(u.ID)
	defer unsubscribe()
	_ = g.Presence.ClearReconnect(r.Context(), u.ID)
	sendOutbound := func(ev protocol.Event) bool {
		select {
		case outbound <- ev:
			return true
		case <-stop:
			return false
		case <-done:
			return false
		}
	}
	go func() {
		defer workers.Done()
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case ev := <-outbound:
				_ = c.SetWriteDeadline(time.Now().Add(g.writeTimeout))
				if err := c.WriteJSON(ev); err != nil {
					return
				}
			}
		}
	}()
	go func() {
		defer workers.Done()
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					return
				}
				select {
				case outbound <- ev:
				case <-stop:
					return
				case <-done:
					return
				}
			case <-stop:
				return
			case <-done:
				return
			}
		}
	}()

	if !sendOutbound(protocol.Event{Type: "hello", Payload: map[string]any{"server_time_ms": time.Now().UnixMilli(), "user": u}}) {
		return
	}
	lastHeartbeat := time.Now()
	requestIDs := make(map[string]struct{})
	windowStarted, commandsInWindow := time.Now(), 0
	_, needsResync := g.Battles.BattleForUser(u.ID)
	for {
		_ = c.SetReadDeadline(time.Now().Add(25 * time.Second))
		var msg protocol.Message
		if err := c.ReadJSON(&msg); err != nil {
			return
		}
		if msg.Payload == nil {
			msg.Payload = map[string]any{}
		}
		switch msg.Type {
		case "ping":
			lastHeartbeat = time.Now()
			if ok, _ := g.Presence.RefreshOnline(r.Context(), u.ID, u.SessionID, g.OnlineTTL); !ok {
				_ = sendOutbound(protocol.Event{Type: "error", Payload: map[string]any{"code": "SESSION_EXPIRED", "message": "session is no longer active"}})
				return
			}
			if !sendOutbound(protocol.Event{Type: "pong", Payload: map[string]any{"server_time_ms": time.Now().UnixMilli()}}) {
				return
			}
		case "match.join", "match.cancel", "room.create", "room.join", "room.ready", "room.start", "room.leave":
			event, err := g.handleLobby(r.Context(), u, msg)
			if err != nil {
				if !sendOutbound(protocol.Event{Type: "error", Payload: map[string]any{"code": "INVALID_STATE", "message": err.Error(), "request_id": msg.RequestID}}) {
					return
				}
			} else if event.Type != "" {
				if !sendOutbound(event) {
					return
				}
			}
		case "battle.move_start", "battle.move_stop", "battle.aim_start", "battle.aim_stop", "battle.select_weapon", "battle.fire", "battle.leave", "battle.resync", "battle.resync_ack":
			if msg.Type == "battle.resync_ack" {
				needsResync = false
				continue
			}
			if needsResync && msg.Type != "battle.resync" {
				if !sendOutbound(protocol.Event{Type: "error", BattleID: msg.BattleID, Payload: map[string]any{"code": "INVALID_STATE", "message": "resync acknowledgement required", "request_id": msg.RequestID, "retryable": true}}) {
					return
				}
				continue
			}
			if msg.RequestID == "" {
				if !sendOutbound(protocol.Event{Type: "error", BattleID: msg.BattleID, Payload: map[string]any{"code": "INVALID_ARGUMENT", "message": "request_id is required"}}) {
					return
				}
				continue
			}
			if _, duplicate := requestIDs[msg.RequestID]; duplicate {
				continue
			}
			requestIDs[msg.RequestID] = struct{}{}
			if len(requestIDs) > 1024 {
				requestIDs = map[string]struct{}{msg.RequestID: {}}
			}
			if time.Since(windowStarted) >= time.Second {
				windowStarted, commandsInWindow = time.Now(), 0
			}
			commandsInWindow++
			if commandsInWindow > 30 {
				if !sendOutbound(protocol.Event{Type: "error", BattleID: msg.BattleID, Payload: map[string]any{"code": "RATE_LIMITED", "message": "command rate exceeded", "request_id": msg.RequestID, "retryable": true}}) {
					return
				}
				continue
			}
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			err := g.Battles.Submit(ctx, u.ID, msg)
			cancel()
			if msg.Type == "battle.leave" && err == nil {
				intentionalLeave = true
			}
			if err != nil {
				if !sendOutbound(protocol.Event{Type: "error", BattleID: msg.BattleID, Payload: map[string]any{"code": codeFor(err), "message": err.Error(), "request_id": msg.RequestID}}) {
					return
				}
			}
		default:
			if !sendOutbound(protocol.Event{Type: "error", BattleID: msg.BattleID, Payload: map[string]any{"code": "UNSUPPORTED_MESSAGE", "message": "unsupported message", "request_id": msg.RequestID}}) {
				return
			}
		}
		if time.Since(lastHeartbeat) > 25*time.Second {
			return
		}
		select {
		case <-done:
			return
		default:
		}
	}
}

func (g *Gateway) handleLobby(ctx context.Context, u service.User, msg protocol.Message) (protocol.Event, error) {
	switch msg.Type {
	case "match.join":
		count := int(number(msg.Payload["player_count"]))
		matchRequestID := stringValue(msg.Payload["match_request_id"])
		if matchRequestID == "" {
			matchRequestID = msg.RequestID
		}
		users, ready, err := g.Lobby.JoinQueueRequest(u.ID, matchRequestID, count)
		if err != nil {
			return protocol.Event{Type: "error", Payload: map[string]any{"code": "MATCH_JOIN_FAILED", "message": err.Error(), "request_id": msg.RequestID}}, nil
		}
		if !ready {
			log.Printf("match.join queue.waiting user_id=%s match_request_id=%s player_count=%d battle_id=", u.ID, matchRequestID, count)
			return protocol.Event{Type: "match.waiting", Payload: map[string]any{"player_count": count, "match_request_id": matchRequestID}}, nil
		}
		log.Printf("match.join queue.ready user_id=%s match_request_id=%s player_count=%d", u.ID, matchRequestID, count)
		players := make([]battle.Player, 0, len(users))
		for _, id := range users {
			player, ok := g.Auth.UserByID(id)
			if !ok {
				g.Lobby.RestoreQueue(users)
				for _, recipient := range users {
					rid, _ := g.Lobby.RequestForUser(recipient)
					g.emitUser(recipient, protocol.Event{Type: "match.failed", Payload: map[string]any{"code": "BATTLE_CREATE_FAILED", "message": "matched user unavailable; returned to matchmaking queue", "match_request_id": rid}})
				}
				return protocol.Event{}, nil
			}
			players = append(players, battle.Player{UserID: id, Username: player.Username})
		}
		log.Printf("match.join battle.create.begin user_id=%s match_request_id=%s player_count=%d", u.ID, matchRequestID, count)
		state, _, err := g.Battles.Create(ctx, "matchmaking", players, time.Now().UnixNano())
		if err != nil {
			g.Lobby.RestoreQueue(users)
			log.Printf("match.join battle.create.failure user_id=%s match_request_id=%s player_count=%d battle_id=", u.ID, matchRequestID, count)
			for _, id := range users {
				rid, _ := g.Lobby.RequestForUser(id)
				g.emitUser(id, protocol.Event{Type: "match.failed", Payload: map[string]any{"code": "BATTLE_CREATE_FAILED", "message": "unable to create battle; returned to matchmaking queue", "match_request_id": rid}})
			}
			return protocol.Event{}, nil
		}
		for _, id := range users {
			g.Lobby.RemoveReservation(id)
		}
		log.Printf("match.join battle.create.success user_id=%s match_request_id=%s player_count=%d battle_id=%s", u.ID, matchRequestID, count, state.BattleID)
		return protocol.Event{}, nil
	case "match.cancel":
		requestID := stringValue(msg.Payload["match_request_id"])
		if activeBattle, ok := g.Battles.BattleForUser(u.ID); ok {
			return protocol.Event{Type: "error", Payload: map[string]any{"code": "BATTLE_ALREADY_STARTED", "battle_id": activeBattle}}, nil
		}
		if current, ok := g.Lobby.RequestForUser(u.ID); ok && requestID != "" && current != requestID {
			log.Printf("match.cancel.failure user_id=%s match_request_id=%s player_count= battle_id=", u.ID, requestID)
			return protocol.Event{Type: "error", Payload: map[string]any{"code": "MATCH_REQUEST_MISMATCH", "message": "match request is no longer active", "request_id": msg.RequestID}}, nil
		}
		g.Lobby.CancelQueue(u.ID)
		g.Lobby.RemoveReservation(u.ID)
		log.Printf("match.cancel user_id=%s match_request_id=%s player_count=", u.ID, requestID)
		log.Printf("match.cancelled user_id=%s match_request_id=%s player_count= battle_id=", u.ID, requestID)
		return protocol.Event{Type: "match.cancelled", Payload: map[string]any{"match_request_id": requestID}}, nil
	case "room.create":
		room, err := g.Lobby.CreateRoom(u.ID, stringValue(msg.Payload["name"]), int(number(msg.Payload["max_players"])))
		return protocol.Event{Type: "room.snapshot", Payload: room}, err
	case "room.join", "room.ready", "room.start", "room.leave":
		roomID := stringValue(msg.Payload["room_id"])
		var room service.Room
		var err error
		closed := false
		switch msg.Type {
		case "room.join":
			room, err = g.Lobby.JoinRoom(roomID, u.ID)
		case "room.ready":
			room, err = g.Lobby.SetReady(roomID, u.ID, boolValue(msg.Payload["ready"]))
		case "room.leave":
			room, closed, err = g.Lobby.LeaveRoom(roomID, u.ID)
		case "room.start":
			room, err = g.Lobby.StartRoom(roomID, u.ID)
		}
		if err != nil {
			return protocol.Event{}, err
		}
		if msg.Type == "room.start" {
			players := make([]battle.Player, 0, len(room.Players))
			for _, id := range room.Players {
				player, ok := g.Auth.UserByID(id)
				if !ok {
					return protocol.Event{}, fmt.Errorf("room user unavailable")
				}
				players = append(players, battle.Player{UserID: id, Username: player.Username})
			}
			_, _, err = g.Battles.Create(ctx, "room", players, time.Now().UnixNano())
			return protocol.Event{}, err
		}
		typeName := "room.snapshot"
		if closed {
			typeName = "room.closed"
		}
		event := protocol.Event{Type: typeName, Payload: room}
		for _, id := range room.Players {
			g.Battles.SendUser(id, event)
		}
		return protocol.Event{}, nil
	}
	return protocol.Event{}, fmt.Errorf("unsupported lobby message")
}
func number(value any) float64     { result, _ := value.(float64); return result }
func stringValue(value any) string { result, _ := value.(string); return result }
func boolValue(value any) bool     { result, _ := value.(bool); return result }

func codeFor(err error) string {
	switch err {
	case battle.ErrManagerShuttingDown:
		return "SERVICE_UNAVAILABLE"
	case battle.ErrBattleNotFound:
		return "INVALID_STATE"
	case battle.ErrUserNotInBattle:
		return "UNAUTHENTICATED"
	case engine.ErrBattlePaused:
		return "BATTLE_PAUSED"
	default:
		return "INVALID_ARGUMENT"
	}
}
