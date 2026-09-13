package httpx

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

type LogOptions struct {
	Logger *slog.Logger
	Redact func(path string) string
	Skip   func(r *http.Request) bool
}

type responseRecorder struct {
	http.ResponseWriter
	status   int
	bytes    int64
	hijacked bool
}

func (r *responseRecorder) WriteHeader(code int) {
	if r.status == 0 && code >= 200 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

func (r *responseRecorder) Written() bool {
	return r.status != 0 || r.hijacked
}

func (r *responseRecorder) Flush() {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("httpx: hijack: %w", http.ErrNotSupported)
	}
	conn, rw, err := h.Hijack()
	if err == nil {
		r.hijacked = true
	}
	return conn, rw, err
}

func (r *responseRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func Logging(opts LogOptions) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if opts.Skip != nil && opts.Skip(r) {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			rec := &responseRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)

			status := rec.StatusCode()
			path := r.URL.Path
			if opts.Redact != nil {
				path = opts.Redact(path)
			}
			level := slog.LevelInfo
			if status >= 500 {
				level = slog.LevelError
			}
			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("path", path),
				slog.Int("status", status),
				slog.Int64("bytes", rec.bytes),
				slog.Duration("duration", time.Since(start)),
				slog.String("request_id", RequestIDFrom(r.Context())),
				slog.String("user_agent", r.UserAgent()),
			}
			if rt := RouteFrom(r.Context()); rt != "" {
				attrs = append(attrs, slog.String("route", rt))
			}
			if ip := ClientIPFrom(r.Context()); ip.IsValid() {
				attrs = append(attrs, slog.String("ip", ip.String()))
			}
			opts.Logger.LogAttrs(r.Context(), level, "http request", attrs...)
		})
	}
}

func RedactPathSegment(prefixes ...string) func(string) string {
	return func(p string) string {
		for _, prefix := range prefixes {
			if !strings.HasPrefix(p, prefix) {
				continue
			}
			rest := p[len(prefix):]
			if rest == "" {
				return p
			}
			if i := strings.IndexAny(rest, "/."); i >= 0 {
				return prefix + ":redacted" + rest[i:]
			}
			return prefix + ":redacted"
		}
		return p
	}
}
