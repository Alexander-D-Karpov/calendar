package ratelimit

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/config"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

var t0 = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func TestLimiterBurstAndRefill(t *testing.T) {
	clk := clock.NewFake(t0)
	l := New(config.Rate{Count: 10, Per: 15 * time.Minute}, clk)

	for i := range 10 {
		d := l.Allow("ip")
		if !d.Allowed || d.Remaining != 9-i || d.Limit != 10 {
			t.Fatalf("request %d = %+v", i, d)
		}
	}
	if d := l.Allow("ip"); d.Allowed || d.RetryAfter != 90*time.Second || d.Remaining != 0 {
		t.Fatalf("over limit = %+v", d)
	}
	if !l.Allow("other").Allowed {
		t.Fatal("keys must be independent")
	}
	clk.Advance(89 * time.Second)
	if d := l.Allow("ip"); d.Allowed || d.RetryAfter != time.Second {
		t.Fatalf("before refill = %+v", d)
	}
	clk.Advance(time.Second)
	if d := l.Allow("ip"); !d.Allowed || d.Remaining != 0 {
		t.Fatalf("after refill = %+v", d)
	}
	l.Reset("ip")
	if d := l.Allow("ip"); !d.Allowed || d.Remaining != 9 {
		t.Fatalf("after reset = %+v", d)
	}
}

func TestLimiterCheckAndBlocked(t *testing.T) {
	clk := clock.NewFake(t0)
	l := New(config.Rate{Count: 2, Per: time.Minute}, clk)
	if _, blocked := l.Blocked("k"); blocked {
		t.Fatal("unknown key must not be blocked")
	}
	_ = l.Check("k")
	if _, blocked := l.Blocked("k"); blocked {
		t.Fatal("one hit must not block")
	}
	_ = l.Check("k")
	wait, blocked := l.Blocked("k")
	if !blocked || wait != 30*time.Second {
		t.Fatalf("Blocked = %v %v", wait, blocked)
	}
	err := l.Check("k")
	var rl *Error
	if !errors.As(err, &rl) || !errors.Is(err, domain.ErrRateLimited) || rl.Error() != "too many attempts, retry in 30s" {
		t.Fatalf("err = %v", err)
	}
}

func TestLimiterSweep(t *testing.T) {
	clk := clock.NewFake(t0)
	l := New(config.Rate{Count: 1, Per: time.Minute}, clk)
	for i := range 100 {
		l.Allow(fmt.Sprint("k", i))
	}
	clk.Advance(2 * time.Minute)
	for range sweepEvery {
		l.Allow("live")
	}
	if l.Len() != 1 {
		t.Fatalf("Len after sweep = %d", l.Len())
	}
}

func TestLimiterFullEvictsLeastLimited(t *testing.T) {
	clk := clock.NewFake(t0)
	l := New(config.Rate{Count: 10, Per: time.Hour}, clk)
	for range 10 {
		l.Allow("hot")
	}
	for i := range maxKeys - 1 {
		l.Allow(fmt.Sprint(i))
	}
	if !l.Allow("new").Allowed {
		t.Fatal("a new key must be admitted when the table is full")
	}
	if l.Allow("hot").Allowed {
		t.Fatal("the most limited key must survive eviction")
	}
	if n := l.Len(); n > maxKeys-maxKeys/evictFraction+1 {
		t.Fatalf("Len after eviction = %d", n)
	}
}

func TestMiddleware(t *testing.T) {
	l := New(config.Rate{Count: 2, Per: time.Minute}, clock.NewFake(t0))
	fail := func(w http.ResponseWriter, _ *http.Request, err error) {
		http.Error(w, err.Error(), http.StatusTooManyRequests)
	}
	ok := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	h := Middleware(l, func(*http.Request) string { return "k" }, fail)(ok)
	send := func(h http.Handler) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		return rec
	}
	rec := send(h)
	if rec.Code != 200 || rec.Header().Get("RateLimit-Limit") != "2" || rec.Header().Get("RateLimit-Remaining") != "1" {
		t.Fatalf("first = %d %v", rec.Code, rec.Header())
	}
	send(h)
	rec = send(h)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "30" || rec.Header().Get("RateLimit-Remaining") != "0" {
		t.Fatalf("limited = %d %v", rec.Code, rec.Header())
	}
	skip := Middleware(l, func(*http.Request) string { return "" }, fail)(ok)
	if rec := send(skip); rec.Code != 200 || rec.Header().Get("RateLimit-Limit") != "" {
		t.Fatalf("empty key = %d", rec.Code)
	}
}
