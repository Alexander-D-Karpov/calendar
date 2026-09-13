package netx

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func TestIsPublic(t *testing.T) {
	cases := map[string]bool{
		"8.8.8.8":              true,
		"93.184.216.34":        true,
		"185.170.196.188":      true,
		"2606:4700:4700::1111": true,
		"2a00:1450:4001::1":    true,
		"64:ff9b::808:808":     true,
		"2002:808:808::":       true,
		"127.0.0.1":            false,
		"127.8.9.10":           false,
		"10.1.2.3":             false,
		"172.16.0.1":           false,
		"172.31.255.255":       false,
		"192.168.1.1":          false,
		"169.254.169.254":      false,
		"100.64.1.1":           false,
		"0.0.0.0":              false,
		"0.1.2.3":              false,
		"192.0.2.10":           false,
		"198.18.0.1":           false,
		"224.0.0.1":            false,
		"255.255.255.255":      false,
		"::":                   false,
		"::1":                  false,
		"::1.2.3.4":            false,
		"::ffff:127.0.0.1":     false,
		"::ffff:10.0.0.1":      false,
		"::ffff:0:a00:1":       false,
		"64:ff9b::a00:1":       false,
		"64:ff9b::7f00:1":      false,
		"2002:a00:1::":         false,
		"2001::1":              false,
		"2001:db8::1":          false,
		"fc00::1":              false,
		"fd12:3456::1":         false,
		"fe80::1":              false,
		"fe80::1%eth0":         false,
		"fec0::1":              false,
		"ff02::1":              false,
	}
	for in, want := range cases {
		a, err := netip.ParseAddr(in)
		if err != nil {
			t.Fatalf("parse %q: %v", in, err)
		}
		if got := IsPublic(a); got != want {
			t.Errorf("IsPublic(%s) = %v, want %v", in, got, want)
		}
	}
	if IsPublic(netip.Addr{}) {
		t.Error("zero address must not be public")
	}
}

func TestGuardControl(t *testing.T) {
	blockedAddrs := []string{"127.0.0.1:80", "[::1]:443", "[fe80::1%eth0]:80", "10.0.0.5:3128", "[::ffff:192.168.0.1]:80"}
	for _, addr := range blockedAddrs {
		if err := guardControl("tcp", addr, nil); !errors.Is(err, ErrBlocked) {
			t.Errorf("guardControl(%s) = %v, want ErrBlocked", addr, err)
		}
	}
	for _, addr := range []string{"93.184.216.34:443", "[2606:4700:4700::1111]:443"} {
		if err := guardControl("tcp", addr, nil); err != nil {
			t.Errorf("guardControl(%s) = %v", addr, err)
		}
	}
	if err := guardControl("tcp", "not-an-address", nil); err == nil || errors.Is(err, ErrBlocked) {
		t.Errorf("malformed address err = %v", err)
	}
}

func TestCheckHost(t *testing.T) {
	res := fakeResolver{
		"ok.test":    {netip.MustParseAddr("93.184.216.34")},
		"mixed.test": {netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("192.168.0.1")},
		"empty.test": {},
	}
	ctx := context.Background()
	if err := checkHost(ctx, res, "ok.test"); err != nil {
		t.Fatalf("ok.test: %v", err)
	}
	var be *BlockedError
	if err := checkHost(ctx, res, "mixed.test"); !errors.As(err, &be) || be.Addr.String() != "192.168.0.1" {
		t.Fatalf("mixed.test err = %v", err)
	}
	if err := checkHost(ctx, res, "10.0.0.1"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("literal err = %v", err)
	}
	if err := checkHost(ctx, res, "8.8.8.8"); err != nil {
		t.Fatalf("public literal: %v", err)
	}
	if err := checkHost(ctx, res, "empty.test"); err == nil || errors.Is(err, ErrBlocked) {
		t.Fatalf("empty.test err = %v", err)
	}
	if err := checkHost(ctx, res, "missing.test"); err == nil || errors.Is(err, ErrBlocked) {
		t.Fatalf("missing.test err = %v", err)
	}
}
