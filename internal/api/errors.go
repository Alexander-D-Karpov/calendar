package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/httpx"
	"github.com/Alexander-D-Karpov/calendar/internal/ratelimit"
)

type Problem struct {
	Type      string              `json:"type"`
	Title     string              `json:"title"`
	Status    int                 `json:"status"`
	Detail    string              `json:"detail,omitempty"`
	Instance  string              `json:"instance,omitempty"`
	RequestID string              `json:"request_id,omitempty"`
	Errors    []domain.FieldError `json:"errors,omitempty"`
}

var sentinels = []error{
	domain.ErrNotFound,
	domain.ErrConflict,
	domain.ErrForbidden,
	domain.ErrUnauthorized,
	domain.ErrPrecondition,
	domain.ErrRateLimited,
	domain.ErrInvalid,
}

func Fail(logger *slog.Logger) auth.Fail {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		WriteProblem(w, r, logger, err)
	}
}

func WriteProblem(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	status := httpx.StatusFor(err)
	p := Problem{
		Type:      "about:blank",
		Title:     http.StatusText(status),
		Status:    status,
		Instance:  r.URL.Path,
		RequestID: httpx.RequestIDFrom(r.Context()),
	}
	h := w.Header()
	var ve *domain.ValidationError
	var rl *ratelimit.Error
	switch {
	case status >= 500:
		logger.LogAttrs(r.Context(), slog.LevelError, "api request failed",
			slog.String("route", httpx.RouteLabel(r.Context())), slog.Any("err", err))
	case errors.As(err, &ve):
		p.Detail = "One or more fields are invalid."
		p.Errors = ve.Fields
	case errors.As(err, &rl):
		ratelimit.SetRetryAfter(h, rl.RetryAfter)
		p.Detail = rl.Error()
	default:
		p.Detail = publicDetail(err)
	}
	if status == http.StatusUnauthorized {
		h.Set("WWW-Authenticate", `Bearer realm="calendar"`)
	}
	h.Set("Content-Type", "application/problem+json")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}

func publicDetail(err error) string {
	var se *httpx.StatusError
	if errors.As(err, &se) {
		return se.Msg
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) || httpx.IsBodyTooLarge(err) {
		return ""
	}
	msg := err.Error()
	for _, s := range sentinels {
		if !errors.Is(err, s) {
			continue
		}
		if rest, ok := strings.CutPrefix(msg, s.Error()+": "); ok {
			return rest
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
