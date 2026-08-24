package redis

import (
	"sync"
	"time"
)

type Lease struct {
	mu    sync.Mutex
	items map[string]time.Time
}

func NewLease() *Lease { return &Lease{items: map[string]time.Time{}} }
func (l *Lease) Acquire(key string, ttl time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if exp, ok := l.items[key]; ok && time.Now().Before(exp) {
		return false
	}
	l.items[key] = time.Now().Add(ttl)
	return true
}
func (l *Lease) Refresh(key string, ttl time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.items[key]; !ok {
		return false
	}
	l.items[key] = time.Now().Add(ttl)
	return true
}
func (l *Lease) Release(key string) { l.mu.Lock(); defer l.mu.Unlock(); delete(l.items, key) }
