package httpx

import "net/http"

type Middleware func(http.Handler) http.Handler

type ctxKey int

const (
	requestIDKey ctxKey = iota
	clientIPKey
)

func Chain(h http.Handler, middleware ...Middleware) http.Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		h = middleware[i](h)
	}
	return h
}
