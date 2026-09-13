package health

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"
)

type Check struct {
	Name string
	Run  func(context.Context) error
}

type readiness struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

func Liveness(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, "ok\n")
}

func Readiness(logger *slog.Logger, timeout time.Duration, checks ...Check) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		resp := readiness{Status: "ok", Checks: make(map[string]string, len(checks))}
		code := http.StatusOK
		for _, c := range checks {
			if err := c.Run(ctx); err != nil {
				logger.LogAttrs(r.Context(), slog.LevelWarn, "readiness check failed",
					slog.String("check", c.Name), slog.Any("err", err))
				resp.Checks[c.Name] = "fail"
				resp.Status = "unavailable"
				code = http.StatusServiceUnavailable
				continue
			}
			resp.Checks[c.Name] = "ok"
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(resp)
	})
}
