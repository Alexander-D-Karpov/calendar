package main

import "testing"

// HTTP_ADDR is a bind address, which is not always a valid destination: ":8080"
// and "0.0.0.0:8080" have to become a host the container can actually dial.
func TestProbeHost(t *testing.T) {
	cases := map[string]string{
		":8080":            "127.0.0.1:8080",
		"0.0.0.0:8080":     "127.0.0.1:8080",
		"[::]:8080":        "127.0.0.1:8080",
		"127.0.0.1:8080":   "127.0.0.1:8080",
		"localhost:8080":   "localhost:8080",
		"[::1]:8080":       "[::1]:8080",
		"192.168.1.10:900": "192.168.1.10:900",
	}
	for in, want := range cases {
		if got := probeHost(in); got != want {
			t.Errorf("probeHost(%q) = %q, want %q", in, got, want)
		}
	}
}
