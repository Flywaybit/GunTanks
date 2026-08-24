package service

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var ErrUnauthenticated = errors.New("unauthenticated")
var ErrInvalidArgument = errors.New("invalid argument")

type Room struct {
	ID, Name, Host string
	MaxPlayers     int
	Players        []string
	Ready          map[string]bool
	Status         string
}
type Lobby struct {
	mu        sync.Mutex
	queues    map[int][]string
	rooms     map[string]*Room
	userRooms map[string]string
	next      uint64
	accepting bool
	requests map[string]queueReservation
}
type queueReservation struct { RequestID string; Count int }

func (l *Lobby) LeaveRoom(id, user string) (Room, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.rooms[id]
	if !ok {
		return Room{}, false, errors.New("room not found")
	}
	index := -1
	for i, player := range r.Players {
		if player == user {
			index = i
			break
		}
	}
	if index < 0 {
		return Room{}, false, errors.New("not in room")
	}
	r.Players = append(r.Players[:index], r.Players[index+1:]...)
	delete(r.Ready, user)
	delete(l.userRooms, user)
	if len(r.Players) == 0 {
		delete(l.rooms, id)
		r.Status = "closed"
		return *r, true, nil
	}
	if r.Host == user {
		r.Host = r.Players[0]
	}
	return *r, false, nil
}

func NewLobby() *Lobby {
	return &Lobby{queues: map[int][]string{}, rooms: map[string]*Room{}, userRooms: map[string]string{}, accepting: true, requests: map[string]queueReservation{}}
}
func (l *Lobby) StopAccepting() { l.mu.Lock(); l.accepting = false; l.mu.Unlock() }
func (l *Lobby) Accepting() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.accepting }
func (l *Lobby) Clear() { l.mu.Lock(); l.queues = map[int][]string{}; l.rooms = map[string]*Room{}; l.userRooms = map[string]string{}; l.requests = map[string]queueReservation{}; l.mu.Unlock() }
func (l *Lobby) JoinQueueRequest(user, requestID string, count int) ([]string, bool, error) {
	if requestID == "" { return nil, false, errors.New("match_request_id is required") }
	l.mu.Lock(); defer l.mu.Unlock()
	if !l.accepting { return nil, false, errors.New("server shutting down") }
	if prior, ok := l.requests[user]; ok {
		if prior.RequestID == requestID { return nil, false, nil }
		for n, queued := range l.queues { out := queued[:0]; for _, id := range queued { if id != user { out = append(out, id) } }; l.queues[n] = out }
		delete(l.requests, user)
	}
	if count != 2 { return nil, false, errors.New("player_count must be 2") }
	if l.userRooms[user] != "" || l.queuedLocked(user) { return nil, false, errors.New("already in lobby activity") }
	l.requests[user] = queueReservation{RequestID: requestID, Count: count}
	l.queues[count] = append(l.queues[count], user)
	if len(l.queues[count]) < count { return nil, false, nil }
	g := append([]string(nil), l.queues[count][:count]...); l.queues[count] = l.queues[count][count:]
	return g, true, nil
}
func (l *Lobby) RemoveReservation(user string) { l.mu.Lock(); delete(l.requests, user); l.mu.Unlock() }
func (l *Lobby) RestoreQueue(users []string) { l.mu.Lock(); defer l.mu.Unlock(); for _, user := range users { if r, ok := l.requests[user]; ok { l.queues[r.Count] = append(l.queues[r.Count], user) } } }
func (l *Lobby) RequestForUser(user string) (string, bool) { l.mu.Lock(); defer l.mu.Unlock(); r, ok := l.requests[user]; return r.RequestID, ok }
func (l *Lobby) JoinQueue(user string, count int) ([]string, bool, error) {
	return l.JoinQueueRequest(user, fmt.Sprintf("legacy-%d", time.Now().UnixNano()), count)
}
func (l *Lobby) CancelQueue(user string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for n := 2; n <= 4; n++ {
		out := l.queues[n][:0]
		for _, u := range l.queues[n] {
			if u != user {
				out = append(out, u)
			}
		}
		l.queues[n] = out
	}
	delete(l.requests, user)
}
func (l *Lobby) CreateRoom(host, name string, max int) (Room, error) {
	if max < 2 || max > 4 {
		return Room{}, errors.New("max_players must be 2..4")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.accepting { return Room{}, errors.New("server shutting down") }
	if l.userRooms[host] != "" || l.queuedLocked(host) {
		return Room{}, errors.New("already in lobby activity")
	}
	l.next++
	id := fmt.Sprintf("room_%d_%d", time.Now().Unix(), l.next)
	r := &Room{ID: id, Name: name, Host: host, MaxPlayers: max, Players: []string{host}, Ready: map[string]bool{host: false}, Status: "waiting"}
	l.rooms[id] = r
	l.userRooms[host] = id
	return *r, nil
}
func (l *Lobby) JoinRoom(id, user string) (Room, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.accepting { return Room{}, errors.New("server shutting down") }
	r, ok := l.rooms[id]
	if !ok {
		return Room{}, errors.New("room not found")
	}
	if r.Status != "waiting" || len(r.Players) >= r.MaxPlayers {
		return Room{}, errors.New("room full")
	}
	if existing := l.userRooms[user]; existing != "" && existing != id {
		return Room{}, errors.New("already in room")
	}
	if l.queuedLocked(user) {
		return Room{}, errors.New("already queued")
	}
	for _, u := range r.Players {
		if u == user {
			return *r, nil
		}
	}
	r.Players = append(r.Players, user)
	l.userRooms[user] = id
	r.Ready[user] = false
	return *r, nil
}
func (l *Lobby) queuedLocked(user string) bool {
	for n := 2; n <= 4; n++ {
		for _, queued := range l.queues[n] {
			if queued == user {
				return true
			}
		}
	}
	return false
}
func (l *Lobby) SetReady(id, user string, ready bool) (Room, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.accepting { return Room{}, errors.New("server shutting down") }
	r, ok := l.rooms[id]
	if !ok {
		return Room{}, errors.New("room not found")
	}
	found := false
	for _, u := range r.Players {
		if u == user {
			found = true
		}
	}
	if !found {
		return Room{}, errors.New("not in room")
	}
	r.Ready[user] = ready
	return *r, nil
}
func (l *Lobby) StartRoom(id, user string) (Room, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.accepting { return Room{}, errors.New("server shutting down") }
	r, ok := l.rooms[id]
	if !ok {
		return Room{}, errors.New("room not found")
	}
	if r.Host != user {
		return Room{}, errors.New("only host can start")
	}
	if len(r.Players) < 2 {
		return Room{}, errors.New("at least two players")
	}
	for _, u := range r.Players {
		if !r.Ready[u] {
			return Room{}, errors.New("all players must be ready")
		}
	}
	r.Status = "playing"
	return *r, nil
}
