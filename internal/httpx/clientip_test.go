package httpx

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestClientIPResolve(t *testing.T) {
	resolver := NewClientIPResolver([]netip.Prefix{
		netip.MustParsePrefix("127.0.0.1/32"),
		netip.MustParsePrefix("::1/128"),
		netip.MustParsePrefix("10.0.0.0/8"),
	})
	cases := []struct {
		name   string
		remote string
		xff    []string
		realIP string
		want   string
	}{
		{"untrusted remote ignores headers", "203.0.113.5:4000", []string{"1.2.3.4"}, "5.6.7.8", "203.0.113.5"},
		{"trusted proxy single hop", "127.0.0.1:5000", []string{"198.51.100.7"}, "", "198.51.100.7"},
		{"spoofed left entries ignored", "127.0.0.1:5000", []string{"6.6.6.6, 198.51.100.7"}, "", "198.51.100.7"},
		{"chain of trusted proxies", "127.0.0.1:5000", []string{"198.51.100.7, 10.1.2.3"}, "", "198.51.100.7"},
		{"multiple header values", "127.0.0.1:5000", []string{"6.6.6.6", "198.51.100.7, 10.0.0.9"}, "", "198.51.100.7"},
		{"all trusted returns leftmost", "127.0.0.1:5000", []string{"10.0.0.2, 10.0.0.3"}, "", "10.0.0.2"},
		{"real ip header", "127.0.0.1:5000", nil, "198.51.100.9", "198.51.100.9"},
		{"ipv4 mapped remote", "[::ffff:127.0.0.1]:5000", []string{"198.51.100.7"}, "", "198.51.100.7"},
		{"ipv6 loopback proxy", "[::1]:5000", []string{"2001:db8::1"}, "", "2001:db8::1"},
		{"hop with port", "127.0.0.1:5000", []string{"198.51.100.7:1234"}, "", "198.51.100.7"},
		{"bracketed ipv6 hop", "127.0.0.1:5000", []string{"[2001:db8::2]"}, "", "2001:db8::2"},
		{"malformed hop falls back", "127.0.0.1:5000", []string{"garbage"}, "", "127.0.0.1"},
		{"no headers from trusted", "127.0.0.1:5000", nil, "", "127.0.0.1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = c.remote
			for _, v := range c.xff {
				req.Header.Add("X-Forwarded-For", v)
			}
			if c.realIP != "" {
				req.Header.Set("X-Real-IP", c.realIP)
			}
			got := resolver.Resolve(req)
			if got.String() != c.want {
				t.Fatalf("Resolve() = %s, want %s", got, c.want)
			}
		})
	}
}

func TestClientIPMiddleware(t *testing.T) {
	resolver := NewClientIPResolver([]netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")})
	var got netip.Addr
	h := resolver.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = ClientIPFrom(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("X-Forwarded-For", "198.51.100.1")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got.String() != "198.51.100.1" {
		t.Fatalf("ClientIPFrom = %s", got)
	}
	if ClientIPFrom(req.Context()).IsValid() {
		t.Fatal("original request context must not be modified")
	}
}
