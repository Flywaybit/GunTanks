package main

import (
	"context"
	"encoding/json"
	"fmt"
	"guntanks-server/battle"
	"guntanks-server/config"
	"guntanks-server/dao"
	gredis "guntanks-server/redis"
	"guntanks-server/service"
	"guntanks-server/web"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type app struct {
	cfg          config.Config
	auth         *service.Auth
	lobby        *service.Lobby
	store        dao.Store
	battles      *battle.Manager
	mongoReady   bool
	redisReady   bool
	presence     gredis.Presence
	gateway      *web.Gateway
	shuttingDown atomic.Bool
	server       *http.Server
	sessionMu    sync.Mutex
	sessions     map[string]string
}

func main() {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatal(err)
	}
	staticDir, err := resolveStaticDir(cfg.StaticDir)
	if err != nil {
		log.Fatal(err)
	}
	cfg.StaticDir = staticDir
	store, mongoReady := initStore(cfg)
	presence, redisReady := initRedis(cfg)
	if cfg.Environment == "production" && (!mongoReady || !redisReady) {
		log.Fatal("MongoDB and Redis must be reachable in production")
	}
	a := &app{
		cfg: cfg, auth: service.NewAuth(cfg.BcryptCost, cfg.JWTSecret, store), lobby: service.NewLobby(),
		store: store, battles: battle.NewManager(store, cfg.BattleTickHz, time.Duration(cfg.ReconnectGraceSeconds)*time.Second),
		mongoReady: mongoReady, redisReady: redisReady,
		presence: presence, sessions: map[string]string{},
	}
	a.battles.SetTurnTimeout(time.Duration(cfg.TurnTimeoutSeconds) * time.Second)
	a.auth.SetTokenTTL(time.Duration(cfg.AccessTokenTTLSeconds) * time.Second)
	a.battles.SetTerrainPath(filepath.Join(cfg.StaticDir, "assets", "terrain-full.png"))
	a.battles.SetSnapshotPolicy(cfg.TerrainSnapshotEventInterval, time.Duration(cfg.TerrainSnapshotSeconds)*time.Second)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/health/live", func(w http.ResponseWriter, _ *http.Request) { write(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("/api/v1/health/ready", func(w http.ResponseWriter, _ *http.Request) {
		status := http.StatusOK
		state := "ready"
		if !a.mongoReady || !a.redisReady {
			status = http.StatusServiceUnavailable
			state = "not_ready"
		}
		write(w, status, map[string]any{"status": state, "mongo": a.mongoReady, "redis": a.redisReady})
	})
	mux.HandleFunc("/api/v1/auth/register", a.register)
	mux.HandleFunc("/api/v1/auth/login", a.login)
	mux.HandleFunc("/api/v1/auth/logout", a.authenticated(a.logout))
	mux.HandleFunc("/api/v1/me", a.authenticated(a.me))
	mux.HandleFunc("/api/v1/matches", a.authenticated(a.matches))
	mux.HandleFunc("/api/v1/matches/", a.authenticated(a.matchDetail))
	mux.HandleFunc("/api/v1/rooms", a.authenticated(a.rooms))
	mux.HandleFunc("/api/v1/battles/", a.authenticated(a.battleDetail))
	a.gateway = web.NewGateway(a.auth, a.battles, a.lobby, a.presence, cfg.MaxWSMessageBytes, time.Duration(cfg.RedisOnlineTTLSeconds)*time.Second, time.Duration(cfg.ReconnectGraceSeconds)*time.Second)
	a.gateway.SetShutdownTimeouts(time.Duration(cfg.WebSocketShutdownTimeoutSeconds)*time.Second, time.Duration(cfg.WebSocketWriteTimeoutSeconds)*time.Second)
	mux.Handle("/ws", a.gateway)
	staticFiles := http.FileServer(http.Dir(cfg.StaticDir))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.ToLower(r.URL.Path)
		if r.URL.Path == "/" || strings.HasSuffix(path, ".html") || strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".css") {
			w.Header().Set("Cache-Control", "no-store")
		}
		staticFiles.ServeHTTP(w, r)
	}))
	srv := &http.Server{Addr: cfg.WebAddr, Handler: logging(mux), ReadHeaderTimeout: 5 * time.Second}
	a.server = srv
	log.Printf("guntanks server listening on %s, static_dir=%s", cfg.WebAddr, cfg.StaticDir)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Printf("http server stopped: %v", err)
		}
		a.shutdown()
	case <-signals:
		a.shutdown()
	}
}

func (a *app) shutdown() {
	if !a.shuttingDown.CompareAndSwap(false, true) {
		return
	}
	log.Printf("stopping new requests, matchmaking, rooms and battle commands")
	a.lobby.StopAccepting()
	a.gateway.BeginShutdown()
	total := time.Duration(a.cfg.ShutdownTimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), total)
	defer cancel()
	_ = a.httpShutdown(ctx)
	log.Printf("closing WebSocket connections")
	_ = a.gateway.CloseAll(ctx)
	log.Printf("cleaning up battles")
	dataCtx, dataCancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = a.battles.Shutdown(dataCtx)
	dataCancel()
	a.lobby.Clear()
	log.Printf("releasing presence and external resources")
	resourceCtx, resourceCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer resourceCancel()
	a.sessionMu.Lock()
	sessions := make(map[string]string, len(a.sessions))
	for user, generation := range a.sessions {
		sessions[user] = generation
	}
	a.sessions = map[string]string{}
	a.sessionMu.Unlock()
	for user, generation := range sessions {
		_ = a.presence.ReleaseOnline(resourceCtx, user, generation)
	}
	if closer, ok := a.store.(interface{ Close(context.Context) error }); ok {
		_ = closer.Close(resourceCtx)
	}
	if closer, ok := a.presence.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}
func (a *app) httpShutdown(ctx context.Context) error { return a.server.Shutdown(ctx) }
func (a *app) rememberSession(user, generation string) {
	a.sessionMu.Lock()
	a.sessions[user] = generation
	a.sessionMu.Unlock()
}
func (a *app) forgetSession(user, generation string) {
	a.sessionMu.Lock()
	if a.sessions[user] == generation {
		delete(a.sessions, user)
	}
	a.sessionMu.Unlock()
}
func resolveStaticDir(configured string) (string, error) {
	candidates := []string{configured, "guntanks-client", filepath.Join("..", "guntanks-client")}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		absolute, err := filepath.Abs(candidate)
		if err != nil || seen[absolute] {
			continue
		}
		seen[absolute] = true
		if info, err := os.Stat(filepath.Join(absolute, "index.html")); err == nil && !info.IsDir() {
			return absolute, nil
		}
	}
	return "", fmt.Errorf("GunTanks client index.html not found; set STATIC_DIR to the guntanks-client directory")
}
func initStore(cfg config.Config) (dao.Store, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(cfg.MongoURI))
	if err != nil || client.Ping(ctx, nil) != nil {
		if client != nil {
			_ = client.Disconnect(context.Background())
		}
		return dao.NewMemoryStore(), false
	}
	store := dao.NewMongoStore(client, cfg.MongoDB)
	if err := store.EnsureIndexes(ctx); err != nil {
		_ = client.Disconnect(context.Background())
		log.Printf("mongo index setup failed, using memory store: %v", err)
		return dao.NewMemoryStore(), false
	}
	return store, true
}
func initRedis(cfg config.Config) (gredis.Presence, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client := gredis.NewClient(cfg.RedisAddr, cfg.RedisPassword, 0)
	if client.Ping(ctx) != nil {
		return gredis.NewMemoryPresence(), false
	}
	return client, true
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func (a *app) current(r *http.Request) (service.User, error) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return service.User{}, service.ErrUnauthenticated
	}
	return a.auth.Verify(strings.TrimPrefix(h, "Bearer "))
}
func (a *app) authenticated(next func(http.ResponseWriter, *http.Request, service.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.shuttingDown.Load() {
			write(w, http.StatusServiceUnavailable, map[string]string{"code": "SERVICE_UNAVAILABLE", "message": "server shutting down"})
			return
		}
		u, e := a.current(r)
		if e != nil {
			write(w, 401, map[string]string{"code": "UNAUTHENTICATED", "message": "authentication required"})
			return
		}
		next(w, r, u)
	}
}
func (a *app) register(w http.ResponseWriter, r *http.Request) {
	if a.shuttingDown.Load() {
		write(w, http.StatusServiceUnavailable, map[string]string{"code": "SERVICE_UNAVAILABLE", "message": "server shutting down"})
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		write(w, 400, map[string]string{"code": "INVALID_ARGUMENT", "message": "invalid JSON request body"})
		return
	}
	if strings.TrimSpace(in.Username) == "" {
		write(w, 400, map[string]string{"code": "INVALID_ARGUMENT", "message": "username is required"})
		return
	}
	if strings.TrimSpace(in.Password) == "" {
		write(w, 400, map[string]string{"code": "INVALID_ARGUMENT", "message": "password is required"})
		return
	}
	u, e := a.auth.Register(in.Username, in.Password)
	if e != nil {
		write(w, 409, map[string]string{"message": "username unavailable"})
		return
	}
	t, _ := a.auth.Token(u)
	session, _ := a.auth.Verify(t)
	online, _ := a.presence.AcquireOnline(r.Context(), u.ID, session.SessionID, time.Duration(a.cfg.RedisOnlineTTLSeconds)*time.Second)
	if !online {
		if battleID, recoverable, _ := a.presence.ReconnectBattle(r.Context(), u.ID); recoverable {
			if _, active := a.battles.Snapshot(battleID); active {
				_ = a.presence.ReplaceOnline(r.Context(), u.ID, session.SessionID, time.Duration(a.cfg.RedisOnlineTTLSeconds)*time.Second)
				_ = a.presence.ClearReconnect(r.Context(), u.ID)
				online = true
			}
		}
	}
	if !online {
		write(w, 409, map[string]string{"code": "ALREADY_ONLINE", "message": "account already online"})
		return
	}
	a.rememberSession(u.ID, session.SessionID)
	write(w, 200, map[string]any{"user": u, "access_token": t})
}
func (a *app) login(w http.ResponseWriter, r *http.Request) {
	if a.shuttingDown.Load() {
		write(w, http.StatusServiceUnavailable, map[string]string{"code": "SERVICE_UNAVAILABLE", "message": "server shutting down"})
		return
	}
	var in struct{ Username, Password string }
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		write(w, 400, map[string]string{"message": "invalid json"})
		return
	}
	if strings.TrimSpace(in.Username) == "" || strings.TrimSpace(in.Password) == "" {
		write(w, 400, map[string]string{"code": "INVALID_ARGUMENT", "message": "username and password are required"})
		return
	}
	u, e := a.auth.Login(in.Username, in.Password)
	if e != nil {
		write(w, 401, map[string]string{"message": "invalid credentials"})
		return
	}
	t, _ := a.auth.Token(u)
	session, _ := a.auth.Verify(t)
	online, _ := a.presence.AcquireOnline(r.Context(), u.ID, session.SessionID, time.Duration(a.cfg.RedisOnlineTTLSeconds)*time.Second)
	if !online {
		if battleID, recoverable, _ := a.presence.ReconnectBattle(r.Context(), u.ID); recoverable {
			if _, active := a.battles.Snapshot(battleID); active {
				_ = a.presence.ReplaceOnline(r.Context(), u.ID, session.SessionID, time.Duration(a.cfg.RedisOnlineTTLSeconds)*time.Second)
				_ = a.presence.ClearReconnect(r.Context(), u.ID)
				online = true
			}
		}
	}
	if !online {
		write(w, 409, map[string]string{"code": "ALREADY_ONLINE", "message": "account already online"})
		return
	}
	a.rememberSession(u.ID, session.SessionID)
	write(w, 200, map[string]any{"user": u, "access_token": t})
}
func (a *app) logout(w http.ResponseWriter, r *http.Request, u service.User) {
	_ = a.presence.ReleaseOnline(r.Context(), u.ID, u.SessionID)
	_ = a.presence.ClearReconnect(r.Context(), u.ID)
	a.forgetSession(u.ID, u.SessionID)
	write(w, 200, map[string]string{"status": "logged_out"})
}
func (a *app) me(w http.ResponseWriter, _ *http.Request, u service.User) {
	write(w, 200, map[string]any{"user": u})
}
func (a *app) matches(w http.ResponseWriter, r *http.Request, u service.User) {
	if r.Method != "POST" {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		recs, err := a.store.ListBattlesForUser(r.Context(), u.ID, limit)
		if err != nil {
			write(w, 500, map[string]string{"code": "INTERNAL", "message": "failed to load matches"})
			return
		}
		write(w, 200, recs)
		return
	}
	var in struct {
		PlayerCount int `json:"player_count"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	g, ready, e := a.lobby.JoinQueue(u.ID, in.PlayerCount)
	if e != nil {
		write(w, 400, map[string]string{"message": e.Error()})
		return
	}
	if !ready {
		write(w, 202, map[string]any{"status": "waiting", "player_count": in.PlayerCount})
		return
	}
	players := make([]battle.Player, 0, len(g))
	for _, id := range g {
		pu, ok := a.auth.UserByID(id)
		if !ok {
			write(w, 409, map[string]string{"code": "INVALID_STATE", "message": "queued user is no longer available"})
			return
		}
		players = append(players, battle.Player{UserID: pu.ID, Username: pu.Username})
	}
	s, mapped, err := a.battles.Create(r.Context(), "matchmaking", players, time.Now().UnixNano())
	if err != nil {
		a.lobby.RestoreQueue(g)
		write(w, 500, map[string]string{"code": "INTERNAL", "message": "failed to create battle"})
		return
	}
	for _, id := range g {
		a.lobby.RemoveReservation(id)
	}
	write(w, 201, map[string]any{"status": "matched", "battle_id": s.BattleID, "players": mapped, "state": s})
}
func (a *app) matchDetail(w http.ResponseWriter, r *http.Request, u service.User) {
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/matches/")
	if i := strings.IndexByte(id, '/'); i >= 0 {
		id = id[:i]
	}
	if strings.HasSuffix(r.URL.Path, "/events") {
		if !a.isParticipant(r, id, u.ID) {
			write(w, 403, map[string]string{"code": "FORBIDDEN", "message": "not a battle participant"})
			return
		}
		after, _ := strconv.ParseUint(r.URL.Query().Get("after_seq"), 10, 64)
		events, err := a.store.ListEvents(r.Context(), id, after)
		if err != nil {
			write(w, 500, map[string]string{"message": "failed to load events"})
			return
		}
		write(w, 200, events)
		return
	}
	rec, err := a.store.GetBattle(r.Context(), id)
	if err != nil {
		write(w, 404, map[string]string{"message": "battle not found"})
		return
	}
	if !recordHasUser(rec, u.ID) {
		write(w, 403, map[string]string{"code": "FORBIDDEN", "message": "not a battle participant"})
		return
	}
	write(w, 200, rec)
}
func (a *app) rooms(w http.ResponseWriter, r *http.Request, u service.User) {
	switch r.Method {
	case "POST":
		var in struct {
			Name       string `json:"name"`
			MaxPlayers int    `json:"max_players"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		room, e := a.lobby.CreateRoom(u.ID, in.Name, in.MaxPlayers)
		if e != nil {
			write(w, 400, map[string]string{"message": e.Error()})
			return
		}
		write(w, 201, room)
	case "PATCH":
		var in struct {
			RoomID string `json:"room_id"`
			Action string `json:"action"`
			Ready  bool   `json:"ready"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		var room service.Room
		var e error
		if in.Action == "join" {
			room, e = a.lobby.JoinRoom(in.RoomID, u.ID)
		} else if in.Action == "ready" {
			room, e = a.lobby.SetReady(in.RoomID, u.ID, in.Ready)
		} else if in.Action == "start" {
			room, e = a.lobby.StartRoom(in.RoomID, u.ID)
		} else if in.Action == "leave" {
			var closed bool
			room, closed, e = a.lobby.LeaveRoom(in.RoomID, u.ID)
			if e == nil && closed {
				write(w, 200, map[string]any{"room": room, "closed": true})
				return
			}
		} else {
			e = service.ErrInvalidArgument
		}
		if e != nil {
			write(w, 400, map[string]string{"message": e.Error()})
			return
		}
		if in.Action == "start" {
			players := make([]battle.Player, 0, len(room.Players))
			for _, id := range room.Players {
				pu, ok := a.auth.UserByID(id)
				if !ok {
					write(w, 409, map[string]string{"code": "INVALID_STATE", "message": "room user is no longer available"})
					return
				}
				players = append(players, battle.Player{UserID: pu.ID, Username: pu.Username})
			}
			state, mapped, err := a.battles.Create(r.Context(), "room", players, time.Now().UnixNano())
			if err != nil {
				write(w, 500, map[string]string{"code": "INTERNAL", "message": "failed to create battle"})
				return
			}
			write(w, 200, map[string]any{"room": room, "battle_id": state.BattleID, "players": mapped, "state": state})
			return
		}
		write(w, 200, room)
	default:
		write(w, 405, nil)
	}
}
func (a *app) battleDetail(w http.ResponseWriter, r *http.Request, u service.User) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/battles/")
	if strings.HasSuffix(path, "/terrain-snapshot") {
		id := strings.TrimSuffix(path, "/terrain-snapshot")
		id = strings.TrimSuffix(id, "/")
		if !a.isParticipant(r, id, u.ID) {
			write(w, 403, map[string]string{"code": "FORBIDDEN", "message": "not a battle participant"})
			return
		}
		snap, err := a.battles.LatestTerrainSnapshot(r.Context(), id)
		if err != nil {
			write(w, 404, map[string]string{"message": "terrain snapshot not found"})
			return
		}
		write(w, 200, snap)
		return
	}
	id := path
	if !a.isParticipant(r, id, u.ID) {
		write(w, 403, map[string]string{"code": "FORBIDDEN", "message": "not a battle participant"})
		return
	}
	s, ok := a.battles.Snapshot(id)
	if !ok {
		rec, err := a.store.GetBattle(r.Context(), id)
		if err == nil {
			write(w, 200, rec)
			return
		}
		write(w, 404, map[string]string{"message": "battle not found"})
		return
	}
	write(w, 200, s)
}
func recordHasUser(rec dao.BattleRecord, userID string) bool {
	for _, p := range rec.Players {
		if p.UserID == userID {
			return true
		}
	}
	return false
}
func (a *app) isParticipant(r *http.Request, battleID, userID string) bool {
	if _, ok := a.battles.TankForUser(battleID, userID); ok {
		return true
	}
	rec, err := a.store.GetBattle(r.Context(), battleID)
	return err == nil && recordHasUser(rec, userID)
}
func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
