package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/ratelimit"
)

func TestWriteProblem(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	cases := []struct {
		name   string
		err    error
		status int
		detail string
	}{
		{"validation", &domain.ValidationError{Fields: []domain.FieldError{{Field: "name", Message: "is required"}}}, 422, "One or more fields are invalid."},
		{"token", auth.ErrInvalidToken, 401, "invalid api token"},
		{"scope", fmt.Errorf("%w: token lacks scope todos:write", domain.ErrForbidden), 403, "token lacks scope todos:write"},
		{"not found", domain.ErrNotFound, 404, ""},
		{"db conflict", fmt.Errorf("%w: %w", domain.ErrConflict, &pgconn.PgError{Message: "users_email_key"}), 409, ""},
		{"internal", errors.New("dial 10.0.0.1: refused"), 500, ""},
		{"rate", &ratelimit.Error{RetryAfter: 1500 * time.Millisecond}, 429, "too many attempts, retry in 2s"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		WriteProblem(rec, httptest.NewRequest(http.MethodGet, "/api/v1/x", nil), logger, c.err)
		var p Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if rec.Code != c.status || p.Status != c.status || p.Detail != c.detail || p.Instance != "/api/v1/x" {
			t.Errorf("%s: %d %+v", c.name, rec.Code, p)
		}
		if rec.Header().Get("Content-Type") != "application/problem+json" {
			t.Errorf("%s: content type %q", c.name, rec.Header().Get("Content-Type"))
		}
		if strings.Contains(rec.Body.String(), "10.0.0.1") || strings.Contains(rec.Body.String(), "users_email_key") {
			t.Errorf("%s: leaked internals: %s", c.name, rec.Body)
		}
	}

	rec := httptest.NewRecorder()
	WriteProblem(rec, httptest.NewRequest(http.MethodGet, "/", nil), logger, auth.ErrInvalidToken)
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("401 must set WWW-Authenticate")
	}
	rec = httptest.NewRecorder()
	WriteProblem(rec, httptest.NewRequest(http.MethodGet, "/", nil), logger, &ratelimit.Error{RetryAfter: 1500 * time.Millisecond})
	if rec.Header().Get("Retry-After") != "2" {
		t.Errorf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("code = %d", rec.Code)
	}
}
