package auth

import (
	"sync"
	"time"
)

type rateEntry struct {
	window time.Time
	count  int
}

type rateLimiter struct {
	mu       sync.Mutex
	entries  map[string]rateEntry
	limit    int
	capacity int
	window   time.Duration
}

func newRateLimiter(limit, capacity int, window time.Duration) *rateLimiter {
	return &rateLimiter{entries: make(map[string]rateEntry), limit: limit, capacity: capacity, window: window}
}

func (l *rateLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if entry, ok := l.entries[key]; ok {
		if now.Sub(entry.window) < l.window {
			if entry.count >= l.limit {
				return false
			}
			entry.count++
			l.entries[key] = entry
			return true
		}
		delete(l.entries, key)
	}
	if len(l.entries) >= l.capacity {
		for existing, entry := range l.entries {
			if now.Sub(entry.window) >= l.window {
				delete(l.entries, existing)
			}
		}
	}
	if len(l.entries) >= l.capacity {
		return false
	}
	l.entries[key] = rateEntry{window: now, count: 1}
	return true
}
