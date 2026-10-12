package weather

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
)

func TestCurrentCachesAndKeepsStale(t *testing.T) {
	var calls atomic.Int32
	fail := atomic.Bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		if got := r.URL.Query().Get("latitude"); got != "55.76" {
			t.Errorf("latitude = %q", got)
		}
		w.Write([]byte(`{"current":{"time":1791827100,"temperature_2m":12.8,"weather_code":3,"is_day":1}}`))
	}))
	defer srv.Close()
	clk := clock.NewFake(time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC))
	s := New(srv.Client(), srv.URL, clk)
	ctx := context.Background()

	c := s.Current(ctx, 55.7558, 37.6173)
	if c == nil || c.Temp != 12.8 || c.Code != 3 || !c.Day || c.At.Unix() != 1791827100 {
		t.Fatalf("got %+v", c)
	}
	s.Current(ctx, 55.7612, 37.6173)
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 within ttl", calls.Load())
	}

	fail.Store(true)
	clk.Advance(16 * time.Minute)
	if c := s.Current(ctx, 55.7558, 37.6173); c == nil || c.Temp != 12.8 {
		t.Fatalf("stale reading lost: %+v", c)
	}
	s.Current(ctx, 55.7558, 37.6173)
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2: a failure is not retried at once", calls.Load())
	}
	if s.Current(ctx, 10, 10) != nil {
		t.Fatal("unknown place with a failing upstream should have no weather")
	}
}
