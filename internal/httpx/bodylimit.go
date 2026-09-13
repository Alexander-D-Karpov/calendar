package httpx

import (
	"errors"
	"net/http"
)

func BodyLimit(limit func(*http.Request) int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil && r.Body != http.NoBody {
				if n := limit(r); n > 0 {
					r.Body = http.MaxBytesReader(w, r.Body, n)
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func FixedBodyLimit(n int64) Middleware {
	return BodyLimit(func(*http.Request) int64 { return n })
}

func IsBodyTooLarge(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}
