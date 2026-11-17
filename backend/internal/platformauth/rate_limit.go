//go:build platform

package platformauth

import (
	"sync"
	"time"
)

type loginWindow struct {
	count   int
	expires time.Time
}
type loginLimiter struct {
	mu      sync.Mutex
	windows map[string]loginWindow
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{windows: make(map[string]loginWindow)} }

func (l *loginLimiter) allow(username, ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, window := range l.windows {
		if !now.Before(window.expires) {
			delete(l.windows, key)
		}
	}
	keys := []string{"account:" + digestToken(username), "ip:" + digestToken(ip)}
	limits := []int{10, 30}
	for i, key := range keys {
		window, exists := l.windows[key]
		if exists && window.count >= limits[i] {
			return false
		}
		if !exists && len(l.windows) >= 10000 {
			return false
		}
	}
	for _, key := range keys {
		window := l.windows[key]
		if window.count == 0 {
			window.expires = now.Add(15 * time.Minute)
		}
		window.count++
		l.windows[key] = window
	}
	return true
}
