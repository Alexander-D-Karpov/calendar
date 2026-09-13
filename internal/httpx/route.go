package httpx

import (
	"context"
	"net/http"
)

const Unmatched = "unmatched"

type Router interface {
	Handler(r *http.Request) (http.Handler, string)
}

type routeCtxKey struct{}

type route struct {
	pattern string
}

func Route(router Router) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, pattern := router.Handler(r)
			ctx := context.WithValue(r.Context(), routeCtxKey{}, &route{pattern: pattern})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RouteFrom(ctx context.Context) string {
	if rt, ok := ctx.Value(routeCtxKey{}).(*route); ok {
		return rt.pattern
	}
	return ""
}

func RouteLabel(ctx context.Context) string {
	if p := RouteFrom(ctx); p != "" {
		return p
	}
	return Unmatched
}

func SetRoute(ctx context.Context, pattern string) {
	if rt, ok := ctx.Value(routeCtxKey{}).(*route); ok {
		rt.pattern = pattern
	}
}
