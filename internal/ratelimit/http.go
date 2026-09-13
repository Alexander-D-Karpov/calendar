package ratelimit

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/httpx"
)

func Middleware(l *Limiter, key func(*http.Request) string, fail func(http.ResponseWriter, *http.Request, error)) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			k := key(r)
			if k == "" {
				next.ServeHTTP(w, r)
				return
			}
			d := l.Allow(k)
			SetHeaders(w.Header(), d)
			if !d.Allowed {
				SetRetryAfter(w.Header(), d.RetryAfter)
				fail(w, r, &Error{RetryAfter: d.RetryAfter})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func ByIP(r *http.Request) string {
	if ip := httpx.ClientIPFrom(r.Context()); ip.IsValid() {
		return "ip:" + ip.String()
	}
	return ""
}

func SetHeaders(h http.Header, d Decision) {
	h.Set("RateLimit-Limit", strconv.Itoa(d.Limit))
	h.Set("RateLimit-Remaining", strconv.Itoa(d.Remaining))
	h.Set("RateLimit-Reset", strconv.Itoa(ceilSeconds(d.Reset)))
}

func SetRetryAfter(h http.Header, d time.Duration) {
	h.Set("Retry-After", strconv.Itoa(Seconds(d)))
}

func Seconds(d time.Duration) int {
	return max(1, ceilSeconds(d))
}

func ceilSeconds(d time.Duration) int {
	return int((d + time.Second - 1) / time.Second)
}
