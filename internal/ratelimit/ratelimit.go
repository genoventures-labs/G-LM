package ratelimit

import (
	"sync"
	"time"
)

type bucket struct {
	tokens     float64
	lastRefill time.Time
}

type Limiter struct {
	mu      sync.Mutex
	rpm     int
	burst   int
	buckets map[string]*bucket
}

func New(rpm, burst int) *Limiter {
	if rpm <= 0 {
		rpm = 60
	}
	if burst <= 0 {
		burst = rpm / 2
		if burst < 1 {
			burst = 1
		}
	}
	return &Limiter{rpm: rpm, burst: burst, buckets: map[string]*bucket{}}
}

func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &bucket{tokens: float64(l.burst - 1), lastRefill: now}
		return true
	}
	perSec := float64(l.rpm) / 60.0
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * perSec
	if b.tokens > float64(l.burst) {
		b.tokens = float64(l.burst)
	}
	b.lastRefill = now
	if b.tokens < 1 {
		return false
	}
	b.tokens -= 1
	return true
}
