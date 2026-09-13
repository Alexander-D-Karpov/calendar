package httpx

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
)

type ClientIPResolver struct {
	trusted []netip.Prefix
}

func NewClientIPResolver(trusted []netip.Prefix) *ClientIPResolver {
	return &ClientIPResolver{trusted: trusted}
}

func (c *ClientIPResolver) Resolve(r *http.Request) netip.Addr {
	remote := parseHostAddr(r.RemoteAddr)
	if !remote.IsValid() || !c.isTrusted(remote) {
		return remote
	}
	if hops := forwardedHops(r.Header.Values("X-Forwarded-For")); len(hops) > 0 {
		for i := len(hops) - 1; i >= 0; i-- {
			addr := parseHostAddr(hops[i])
			if !addr.IsValid() {
				return remote
			}
			if !c.isTrusted(addr) {
				return addr
			}
		}
		if first := parseHostAddr(hops[0]); first.IsValid() {
			return first
		}
		return remote
	}
	if real := parseHostAddr(r.Header.Get("X-Real-IP")); real.IsValid() {
		return real
	}
	return remote
}

func (c *ClientIPResolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := c.Resolve(r)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientIPKey, ip)))
	})
}

func ClientIPFrom(ctx context.Context) netip.Addr {
	ip, _ := ctx.Value(clientIPKey).(netip.Addr)
	return ip
}

func (c *ClientIPResolver) isTrusted(addr netip.Addr) bool {
	for _, p := range c.trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func forwardedHops(values []string) []string {
	var hops []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				hops = append(hops, part)
			}
		}
	}
	return hops
}

func parseHostAddr(s string) netip.Addr {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Addr{}
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap()
	}
	s = strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap()
	}
	return netip.Addr{}
}
