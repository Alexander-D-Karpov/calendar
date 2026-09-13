package httpx

import (
	"net/http"
	"net/url"
	"strings"
)

const permissionsPolicy = "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()"

type SecurityOptions struct {
	CSP  string
	HSTS bool
}

func SecurityHeaders(opts SecurityOptions) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			if opts.CSP != "" {
				h.Set("Content-Security-Policy", opts.CSP)
			}
			if opts.HSTS {
				h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "same-origin")
			h.Set("Permissions-Policy", permissionsPolicy)
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			next.ServeHTTP(w, r)
		})
	}
}

func BuildCSP(base *url.URL) string {
	wsScheme := "ws"
	if base.Scheme == "https" {
		wsScheme = "wss"
	}
	directives := []string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self'",
		"img-src 'self' data:",
		"font-src 'self'",
		"connect-src 'self' " + wsScheme + "://" + base.Host,
		"worker-src 'self'",
		"manifest-src 'self'",
		"form-action 'self' https://accounts.google.com",
		"frame-ancestors 'none'",
		"base-uri 'none'",
		"object-src 'none'",
	}
	return strings.Join(directives, "; ")
}

func SetCSP(w http.ResponseWriter, csp string) {
	w.Header().Set("Content-Security-Policy", csp)
}

func SetReferrerPolicy(w http.ResponseWriter, policy string) {
	w.Header().Set("Referrer-Policy", policy)
}
