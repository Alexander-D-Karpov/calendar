package health

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReadiness(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ok := Check{Name: "database", Run: func(context.Context) error { return nil }}
	bad := Check{Name: "schema", Run: func(context.Context) error {
		return errors.New("postgres://user:secret@db/calendar unreachable")
	}}

	rec := httptest.NewRecorder()
	Readiness(logger, time.Second, ok).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("ready = %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	Readiness(logger, time.Second, ok, bad).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(body, `"schema":"fail"`) || !strings.Contains(body, `"database":"ok"`) {
		t.Fatalf("unready = %d %s", rec.Code, body)
	}
	if strings.Contains(body, "secret") {
		t.Fatal("readiness must not leak error details")
	}
}

func TestLiveness(t *testing.T) {
	rec := httptest.NewRecorder()
	Liveness(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok\n" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("liveness = %d %q", rec.Code, rec.Body)
	}
}
