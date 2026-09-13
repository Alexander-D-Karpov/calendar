package netx

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync"
	"testing"
	"time"
)

type fakeResolver map[string][]netip.Addr

func (f fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if addrs, ok := f[host]; ok {
		return addrs, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

type recorder struct {
	mu    sync.Mutex
	hosts []string
	last  string
}

func (r *recorder) add(host, auth string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hosts = append(r.hosts, host)
	r.last = auth
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.hosts...)
}

func (r *recorder) auth() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

func startProxy(t *testing.T) (*url.URL, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.URL.Host, r.Header.Get("Proxy-Authorization"))
		_, _ = io.WriteString(w, "proxy:"+r.URL.Host)
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u, rec
}

func startBackend(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "direct")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func mustNetwork(t *testing.T, o Options) *Network {
	t.Helper()
	n, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(n.CloseIdleConnections)
	return n
}

func get(t *testing.T, n *Network, p Purpose, rawURL string) (string, error) {
	t.Helper()
	resp, err := n.Client(p, 5*time.Second).Get(rawURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

func TestProxiedPurposeUsesProxy(t *testing.T) {
	proxy, rec := startProxy(t)
	n := mustNetwork(t, Options{Proxy: proxy, ProxyFor: []Purpose{Google}})

	body, err := get(t, n, Google, "http://calendar.googleapis.test/cal")
	if err != nil {
		t.Fatal(err)
	}
	if body != "proxy:calendar.googleapis.test" {
		t.Fatalf("body = %q", body)
	}
	if got := rec.seen(); len(got) != 1 {
		t.Fatalf("proxy saw %v", got)
	}
	if !n.Proxied(Google) || n.Proxied(Sentry) || n.Proxied(Fetch) {
		t.Fatal("Proxied mismatch")
	}
}

func TestProxyCredentials(t *testing.T) {
	proxy, rec := startProxy(t)
	withAuth := *proxy
	withAuth.User = url.UserPassword("cal", "s3cret")
	n := mustNetwork(t, Options{Proxy: &withAuth, ProxyFor: []Purpose{Google}})

	if _, err := get(t, n, Google, "http://oauth2.googleapis.test/token"); err != nil {
		t.Fatal(err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("cal:s3cret"))
	if got := rec.auth(); got != want {
		t.Fatalf("Proxy-Authorization = %q, want %q", got, want)
	}
	if got := n.ProxyHost(); got != "http://"+proxy.Host {
		t.Fatalf("ProxyHost() = %q", got)
	}
}

func TestUnproxiedPurposeGoesDirect(t *testing.T) {
	proxy, rec := startProxy(t)
	backend := startBackend(t)
	n := mustNetwork(t, Options{Proxy: proxy, ProxyFor: []Purpose{Google}})

	body, err := get(t, n, Sentry, backend.URL)
	if err != nil || body != "direct" {
		t.Fatalf("body = %q, err = %v", body, err)
	}
	if got := rec.seen(); len(got) != 0 {
		t.Fatalf("proxy saw %v", got)
	}
}

func TestLoopbackBypassesProxy(t *testing.T) {
	proxy, rec := startProxy(t)
	backend := startBackend(t)
	n := mustNetwork(t, Options{Proxy: proxy, ProxyFor: []Purpose{Google}})

	body, err := get(t, n, Google, backend.URL)
	if err != nil || body != "direct" {
		t.Fatalf("body = %q, err = %v", body, err)
	}
	if got := rec.seen(); len(got) != 0 {
		t.Fatalf("proxy saw %v", got)
	}
}

func TestNoProxyList(t *testing.T) {
	proxy, rec := startProxy(t)
	n := mustNetwork(t, Options{
		Proxy:    proxy,
		ProxyFor: []Purpose{Google},
		NoProxy:  []string{"calendar.googleapis.test"},
	})
	if _, err := get(t, n, Google, "http://calendar.googleapis.test/"); err == nil {
		t.Fatal("expected direct dial to an unresolvable host to fail")
	}
	if got := rec.seen(); len(got) != 0 {
		t.Fatalf("proxy saw %v", got)
	}
}

func TestGuardBlocksPrivateDirect(t *testing.T) {
	backend := startBackend(t)
	n := mustNetwork(t, Options{})

	for _, p := range []Purpose{Fetch, Push} {
		if _, err := get(t, n, p, backend.URL); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s err = %v, want ErrBlocked", p, err)
		}
	}
	for _, p := range []Purpose{Google, Sentry} {
		if body, err := get(t, n, p, backend.URL); err != nil || body != "direct" {
			t.Errorf("%s body = %q, err = %v", p, body, err)
		}
	}
}

func TestAllowPrivateOnlyAffectsFetch(t *testing.T) {
	backend := startBackend(t)
	n := mustNetwork(t, Options{AllowPrivate: true})

	if body, err := get(t, n, Fetch, backend.URL); err != nil || body != "direct" {
		t.Fatalf("fetch body = %q, err = %v", body, err)
	}
	if _, err := get(t, n, Push, backend.URL); !errors.Is(err, ErrBlocked) {
		t.Fatalf("push err = %v, want ErrBlocked", err)
	}
}

func TestProxiedFetchPreflight(t *testing.T) {
	proxy, rec := startProxy(t)
	res := fakeResolver{
		"feed.test": {netip.MustParseAddr("93.184.216.34")},
		"evil.test": {netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("10.0.0.1")},
	}
	n := mustNetwork(t, Options{Proxy: proxy, ProxyFor: []Purpose{Fetch}, Resolver: res})

	body, err := get(t, n, Fetch, "http://feed.test/cal.ics")
	if err != nil || body != "proxy:feed.test" {
		t.Fatalf("body = %q, err = %v", body, err)
	}

	blockedURLs := []string{
		"http://evil.test/",
		"http://10.1.2.3/",
		"http://169.254.169.254/latest/meta-data/",
		"http://[fe80::1%25eth0]/",
		"http://127.0.0.1:1/",
		proxy.String() + "/",
	}
	for _, u := range blockedURLs {
		if _, err := get(t, n, Fetch, u); !errors.Is(err, ErrBlocked) {
			t.Errorf("%s err = %v, want ErrBlocked", u, err)
		}
	}
	if _, err := get(t, n, Fetch, "http://missing.test/"); err == nil || errors.Is(err, ErrBlocked) {
		t.Errorf("missing.test err = %v, want resolve error", err)
	}
	if got := rec.seen(); len(got) != 1 || got[0] != "feed.test" {
		t.Fatalf("proxy saw %v, want only feed.test", got)
	}
}

func TestNewRejectsUnknownPurpose(t *testing.T) {
	proxy, _ := startProxy(t)
	if _, err := New(Options{Proxy: proxy, ProxyFor: []Purpose{"mail"}}); err == nil {
		t.Fatal("expected error for unknown purpose")
	}
	n := mustNetwork(t, Options{})
	defer func() {
		if recover() == nil {
			t.Fatal("Transport with unknown purpose must panic")
		}
	}()
	n.Transport("mail")
}
