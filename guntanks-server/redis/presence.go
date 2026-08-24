package redis

import (
	"context"
	"sync"
	"time"
)

type Presence interface {
	AcquireOnline(context.Context, string, string, time.Duration) (bool, error)
	RefreshOnline(context.Context, string, string, time.Duration) (bool, error)
	ReleaseOnline(context.Context, string, string) error
	SetReconnect(context.Context, string, string, time.Duration) error
	ClearReconnect(context.Context, string) error
	ReconnectBattle(context.Context, string) (string, bool, error)
	ReplaceOnline(context.Context, string, string, time.Duration) error
}

func (m *MemoryPresence) ReleaseAll(_ context.Context) error {
	m.mu.Lock()
	m.online = map[string]memoryItem{}
	m.reconnect = map[string]memoryItem{}
	m.mu.Unlock()
	return nil
}

type memoryItem struct {
	value   string
	expires time.Time
}
type MemoryPresence struct {
	mu                sync.Mutex
	online, reconnect map[string]memoryItem
}

func NewMemoryPresence() *MemoryPresence {
	return &MemoryPresence{online: map[string]memoryItem{}, reconnect: map[string]memoryItem{}}
}
func (m *MemoryPresence) AcquireOnline(_ context.Context, user, generation string, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if item, ok := m.online[user]; ok && time.Now().Before(item.expires) && item.value != generation {
		return false, nil
	}
	m.online[user] = memoryItem{generation, time.Now().Add(ttl)}
	return true, nil
}
func (m *MemoryPresence) RefreshOnline(_ context.Context, user, generation string, ttl time.Duration) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.online[user]
	if !ok || item.value != generation {
		return false, nil
	}
	item.expires = time.Now().Add(ttl)
	m.online[user] = item
	return true, nil
}
func (m *MemoryPresence) ReleaseOnline(_ context.Context, user, generation string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.online[user].value == generation {
		delete(m.online, user)
	}
	return nil
}
func (m *MemoryPresence) SetReconnect(_ context.Context, user, battle string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reconnect[user] = memoryItem{battle, time.Now().Add(ttl)}
	return nil
}
func (m *MemoryPresence) ClearReconnect(_ context.Context, user string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.reconnect, user)
	return nil
}
func (m *MemoryPresence) ReconnectBattle(_ context.Context, user string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, ok := m.reconnect[user]
	if !ok || time.Now().After(item.expires) {
		if ok {
			delete(m.reconnect, user)
		}
		return "", false, nil
	}
	return item.value, true, nil
}
func (m *MemoryPresence) ReplaceOnline(_ context.Context, user, generation string, ttl time.Duration) error {
	m.mu.Lock()
	m.online[user] = memoryItem{generation, time.Now().Add(ttl)}
	m.mu.Unlock()
	return nil
}
