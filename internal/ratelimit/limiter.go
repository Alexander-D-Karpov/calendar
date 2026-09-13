package ratelimit

import (
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/config"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const (
	sweepEvery    = 1024
	maxKeys       = 100_000
	evictFraction = 10
)

type Decision struct {
	Allowed    bool
	Limit      int
	Remaining  int
	RetryAfter time.Duration
	Reset      time.Duration
}

type Error struct {
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	return fmt.Sprintf("too many attempts, retry in %s", e.RetryAfter.Round(time.Second))
}

func (e *Error) Unwrap() error {
	return domain.ErrRateLimited
}

type Limiter struct {
	mu        sync.Mutex
	clock     clock.Clock
	limit     int
	period    time.Duration
	interval  time.Duration
	tat       map[string]time.Time
	calls     uint64
	lastSweep time.Time
}

func New(rate config.Rate, c clock.Clock) *Limiter {
	if c == nil {
		c = clock.New()
	}
	return &Limiter{
		clock:    c,
		limit:    rate.Count,
		period:   rate.Per,
		interval: rate.Interval(),
		tat:      map[string]time.Time{},
	}
}

func (l *Limiter) Allow(key string) Decision {
	return l.AllowN(key, 1)
}

func (l *Limiter) Check(key string) error {
	if d := l.Allow(key); !d.Allowed {
		return &Error{RetryAfter: d.RetryAfter}
	}
	return nil
}

func (l *Limiter) AllowN(key string, n int) Decision {
	now := l.clock.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)

	tat, known := l.tat[key]
	if !known && len(l.tat) >= maxKeys {
		l.evict(now)
	}
	if tat.Before(now) {
		tat = now
	}
	next := tat.Add(time.Duration(n) * l.interval)
	allowAt := next.Add(-l.period)
	if now.Before(allowAt) {
		return Decision{
			Limit:      l.limit,
			Remaining:  l.remaining(now, tat),
			RetryAfter: allowAt.Sub(now),
			Reset:      tat.Sub(now),
		}
	}
	l.tat[key] = next
	return Decision{
		Allowed:   true,
		Limit:     l.limit,
		Remaining: l.remaining(now, next),
		Reset:     next.Sub(now),
	}
}

func (l *Limiter) Blocked(key string) (time.Duration, bool) {
	now := l.clock.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	tat, ok := l.tat[key]
	if !ok {
		return 0, false
	}
	wait := tat.Add(l.interval - l.period).Sub(now)
	return wait, wait > 0
}

func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.tat, key)
}

func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.tat)
}

func (l *Limiter) remaining(now, tat time.Time) int {
	r := int((l.period - tat.Sub(now)) / l.interval)
	return min(max(r, 0), l.limit)
}

func (l *Limiter) sweep(now time.Time) {
	l.calls++
	if l.calls%sweepEvery != 0 {
		return
	}
	l.expire(now)
}

func (l *Limiter) expire(now time.Time) {
	l.lastSweep = now
	for k, t := range l.tat {
		if !t.After(now) {
			delete(l.tat, k)
		}
	}
}

func (l *Limiter) evict(now time.Time) {
	l.expire(now)
	if len(l.tat) < maxKeys {
		return
	}
	type entry struct {
		key string
		tat time.Time
	}
	all := make([]entry, 0, len(l.tat))
	for k, t := range l.tat {
		all = append(all, entry{key: k, tat: t})
	}
	slices.SortFunc(all, func(a, b entry) int { return a.tat.Compare(b.tat) })
	for _, e := range all[:len(all)/evictFraction] {
		delete(l.tat, e.key)
	}
}
