package netx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/http/httpproxy"
)

type Purpose string

const (
	Google Purpose = "google"
	Push   Purpose = "push"
	Sentry Purpose = "sentry"
	Fetch  Purpose = "fetch"
)

var Purposes = []Purpose{Google, Push, Sentry, Fetch}

var defaultPorts = map[string]string{
	"http":    "80",
	"https":   "443",
	"socks5":  "1080",
	"socks5h": "1080",
}

type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

type Options struct {
	Proxy        *url.URL
	ProxyFor     []Purpose
	NoProxy      []string
	AllowPrivate bool
	Resolver     Resolver
	DialTimeout  time.Duration
}

type Network struct {
	proxy      *url.URL
	proxyAddr  string
	proxied    map[Purpose]bool
	transports map[Purpose]*http.Transport
	trippers   map[Purpose]http.RoundTripper
}

func New(o Options) (*Network, error) {
	if o.Resolver == nil {
		o.Resolver = net.DefaultResolver
	}
	if o.DialTimeout <= 0 {
		o.DialTimeout = 10 * time.Second
	}
	n := &Network{
		proxied:    map[Purpose]bool{},
		transports: map[Purpose]*http.Transport{},
		trippers:   map[Purpose]http.RoundTripper{},
	}

	var proxyFn func(*url.URL) (*url.URL, error)
	if o.Proxy != nil {
		addr, err := canonicalAddr(o.Proxy)
		if err != nil {
			return nil, fmt.Errorf("netx: proxy: %w", err)
		}
		n.proxy = o.Proxy
		n.proxyAddr = addr
		proxyFn = (&httpproxy.Config{
			HTTPProxy:  o.Proxy.String(),
			HTTPSProxy: o.Proxy.String(),
			NoProxy:    strings.Join(o.NoProxy, ","),
		}).ProxyFunc()
		for _, p := range o.ProxyFor {
			if !slices.Contains(Purposes, p) {
				return nil, fmt.Errorf("netx: unknown purpose %q", p)
			}
			n.proxied[p] = true
		}
	}

	for _, p := range Purposes {
		guarded := p == Push || (p == Fetch && !o.AllowPrivate)
		var proxy func(*http.Request) (*url.URL, error)
		if n.proxied[p] {
			proxy = func(r *http.Request) (*url.URL, error) {
				return proxyFn(r.URL)
			}
		}
		t := newTransport(n.dialer(guarded, n.proxied[p], o.DialTimeout), proxy)
		n.transports[p] = t
		if guarded {
			n.trippers[p] = &guardTransport{
				base:      t,
				proxy:     proxy,
				resolver:  o.Resolver,
				proxyAddr: n.proxyAddr,
			}
			continue
		}
		n.trippers[p] = t
	}
	return n, nil
}

func (n *Network) Transport(p Purpose) http.RoundTripper {
	rt, ok := n.trippers[p]
	if !ok {
		panic("netx: unknown purpose " + string(p))
	}
	return rt
}

func (n *Network) Client(p Purpose, timeout time.Duration) *http.Client {
	return &http.Client{Transport: n.Transport(p), Timeout: timeout}
}

func (n *Network) Proxied(p Purpose) bool {
	return n.proxied[p]
}

func (n *Network) ProxyHost() string {
	if n.proxy == nil {
		return ""
	}
	return n.proxy.Scheme + "://" + n.proxy.Host
}

func (n *Network) CloseIdleConnections() {
	for _, t := range n.transports {
		t.CloseIdleConnections()
	}
}

func (n *Network) dialer(guarded, viaProxy bool, timeout time.Duration) func(context.Context, string, string) (net.Conn, error) {
	plain := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	if !guarded {
		return plain.DialContext
	}
	safe := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second, Control: guardControl}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if viaProxy && strings.EqualFold(addr, n.proxyAddr) {
			return plain.DialContext(ctx, network, addr)
		}
		return safe.DialContext(ctx, network, addr)
	}
}

func newTransport(dial func(context.Context, string, string) (net.Conn, error), proxy func(*http.Request) (*url.URL, error)) *http.Transport {
	return &http.Transport{
		Proxy:                 proxy,
		DialContext:           dial,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
}

type guardTransport struct {
	base      *http.Transport
	proxy     func(*http.Request) (*url.URL, error)
	resolver  Resolver
	proxyAddr string
}

func (g *guardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := g.check(req); err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	return g.base.RoundTrip(req)
}

func (g *guardTransport) CloseIdleConnections() {
	g.base.CloseIdleConnections()
}

func (g *guardTransport) check(req *http.Request) error {
	host := req.URL.Hostname()
	if host == "" {
		return errors.New("netx: request has no host")
	}
	if g.proxy != nil {
		p, err := g.proxy(req)
		if err != nil {
			return err
		}
		if p != nil {
			return checkHost(req.Context(), g.resolver, host)
		}
	}
	if g.proxyAddr != "" {
		if addr, err := canonicalAddr(req.URL); err == nil && strings.EqualFold(addr, g.proxyAddr) {
			return fmt.Errorf("%w: %s is the outbound proxy", ErrBlocked, host)
		}
	}
	return nil
}

func canonicalAddr(u *url.URL) (string, error) {
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", errors.New("missing host")
	}
	port := u.Port()
	if port == "" {
		port = defaultPorts[u.Scheme]
		if port == "" {
			return "", fmt.Errorf("unsupported scheme %q", u.Scheme)
		}
	}
	return net.JoinHostPort(host, port), nil
}
