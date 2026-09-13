package netx

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"syscall"
)

var ErrBlocked = errors.New("netx: address is not publicly routable")

type BlockedError struct {
	Host string
	Addr netip.Addr
}

func (e *BlockedError) Error() string {
	if e.Host != "" && e.Host != e.Addr.String() {
		return fmt.Sprintf("netx: %s resolves to %s, which is not publicly routable", e.Host, e.Addr)
	}
	return fmt.Sprintf("netx: %s is not publicly routable", e.Addr)
}

func (e *BlockedError) Unwrap() error {
	return ErrBlocked
}

var (
	nat64     = netip.MustParsePrefix("64:ff9b::/96")
	sixToFour = netip.MustParsePrefix("2002::/16")
	blocked   = mustPrefixes(
		"0.0.0.0/8",
		"10.0.0.0/8",
		"100.64.0.0/10",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"172.16.0.0/12",
		"192.0.0.0/24",
		"192.0.2.0/24",
		"192.88.99.0/24",
		"192.168.0.0/16",
		"198.18.0.0/15",
		"198.51.100.0/24",
		"203.0.113.0/24",
		"224.0.0.0/4",
		"240.0.0.0/4",
		"::/96",
		"::ffff:0:0:0/96",
		"64:ff9b:1::/48",
		"100::/64",
		"2001::/23",
		"2001:db8::/32",
		"3fff::/20",
		"fc00::/7",
		"fe80::/10",
		"fec0::/10",
		"ff00::/8",
	)
)

func mustPrefixes(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

func IsPublic(a netip.Addr) bool {
	if !a.IsValid() {
		return false
	}
	a = a.WithZone("").Unmap()
	if a.Is6() {
		b := a.As16()
		switch {
		case nat64.Contains(a):
			return IsPublic(netip.AddrFrom4([4]byte(b[12:16])))
		case sixToFour.Contains(a):
			return IsPublic(netip.AddrFrom4([4]byte(b[2:6])))
		}
	}
	for _, p := range blocked {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

func guardControl(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("netx: unexpected dial address %q", address)
	}
	if !IsPublic(ap.Addr()) {
		return &BlockedError{Addr: ap.Addr()}
	}
	return nil
}

func checkHost(ctx context.Context, res Resolver, host string) error {
	if a, err := netip.ParseAddr(host); err == nil {
		if !IsPublic(a) {
			return &BlockedError{Host: host, Addr: a}
		}
		return nil
	}
	addrs, err := res.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("netx: resolve %s: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("netx: %s has no addresses", host)
	}
	for _, a := range addrs {
		if !IsPublic(a) {
			return &BlockedError{Host: host, Addr: a}
		}
	}
	return nil
}
