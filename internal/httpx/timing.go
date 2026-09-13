package httpx

import (
	"context"
	"net/http"
	"time"
)

type startKey struct{}

func Timing(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), startKey{}, time.Now())))
	})
}

func StartedAt(ctx context.Context) time.Time {
	t, _ := ctx.Value(startKey{}).(time.Time)
	return t
}
