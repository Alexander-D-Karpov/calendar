package config

import (
	"bytes"
	"encoding/base64"
	"net/netip"
	"testing"
	"time"
)

func TestParseByteSize(t *testing.T) {
	cases := []struct {
		in   string
		want ByteSize
		ok   bool
	}{
		{"1024", 1024, true},
		{"1B", 1, true},
		{"1KB", KB, true},
		{"512kb", 512 * KB, true},
		{"1MB", MB, true},
		{"20M", 20 * MB, true},
		{"2GB", 2 * GB, true},
		{" 3 gb ", 3 * GB, true},
		{"1TB", TB, true},
		{"", 0, false},
		{"MB", 0, false},
		{"-1MB", 0, false},
		{"1.5GB", 0, false},
		{"99999999999TB", 0, false},
	}
	for _, c := range cases {
		got, err := ParseByteSize(c.in)
		if c.ok && (err != nil || got != c.want) {
			t.Errorf("ParseByteSize(%q) = %v, %v, want %v", c.in, got, err, c.want)
		}
		if !c.ok && err == nil {
			t.Errorf("ParseByteSize(%q) expected error, got %v", c.in, got)
		}
	}
}

func TestByteSizeString(t *testing.T) {
	cases := map[ByteSize]string{
		0:       "0B",
		1:       "1B",
		1536:    "1536B",
		KB:      "1KB",
		20 * MB: "20MB",
		2 * GB:  "2GB",
		3 * TB:  "3TB",
		MB + KB: "1025KB",
	}
	for in, want := range cases {
		if got := in.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", int64(in), got, want)
		}
	}
}

func TestParseRate(t *testing.T) {
	cases := []struct {
		in   string
		want Rate
		ok   bool
	}{
		{"10/15m", Rate{10, 15 * time.Minute}, true},
		{"600/1m", Rate{600, time.Minute}, true},
		{"5/m", Rate{5, time.Minute}, true},
		{" 3 / 1h ", Rate{3, time.Hour}, true},
		{"10", Rate{}, false},
		{"0/1m", Rate{}, false},
		{"x/1m", Rate{}, false},
		{"10/soon", Rate{}, false},
		{"10/0s", Rate{}, false},
	}
	for _, c := range cases {
		got, err := ParseRate(c.in)
		if c.ok && (err != nil || got != c.want) {
			t.Errorf("ParseRate(%q) = %v, %v, want %v", c.in, got, err, c.want)
		}
		if !c.ok && err == nil {
			t.Errorf("ParseRate(%q) expected error", c.in)
		}
	}
}

func TestRateStringAndInterval(t *testing.T) {
	r := Rate{Count: 10, Per: 15 * time.Minute}
	if got := r.String(); got != "10/15m" {
		t.Errorf("String() = %q", got)
	}
	if got := r.Interval(); got != 90*time.Second {
		t.Errorf("Interval() = %v", got)
	}
	if got := (Rate{Count: 1, Per: 90 * time.Minute}).String(); got != "1/1h30m" {
		t.Errorf("String() = %q", got)
	}
}

func TestFormatDuration(t *testing.T) {
	cases := map[time.Duration]string{
		720 * time.Hour:  "720h",
		15 * time.Minute: "15m",
		90 * time.Second: "1m30s",
		30 * time.Second: "30s",
		0:                "0s",
	}
	for in, want := range cases {
		if got := formatDuration(in); got != want {
			t.Errorf("formatDuration(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestParseSecretKeys(t *testing.T) {
	k1 := bytes.Repeat([]byte{1}, 32)
	k2 := bytes.Repeat([]byte{2}, 32)
	in := "1:" + base64.StdEncoding.EncodeToString(k1) + ", 2:" + base64.RawURLEncoding.EncodeToString(k2)
	keys, err := ParseSecretKeys(in)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(keys[1], k1) || !bytes.Equal(keys[2], k2) {
		t.Fatal("decoded keys mismatch")
	}
	if keys.Highest() != 2 {
		t.Errorf("Highest() = %d", keys.Highest())
	}
	if got := keys.Masked(); got != "1:****,2:****" {
		t.Errorf("Masked() = %q", got)
	}

	short := base64.StdEncoding.EncodeToString([]byte("short"))
	bad := []string{
		"",
		"nocolon",
		"0:" + base64.StdEncoding.EncodeToString(k1),
		"x:" + base64.StdEncoding.EncodeToString(k1),
		"1:" + short,
		"1:!!!notbase64!!!",
		"1:" + base64.StdEncoding.EncodeToString(k1) + ",1:" + base64.StdEncoding.EncodeToString(k2),
	}
	for _, b := range bad {
		if _, err := ParseSecretKeys(b); err == nil {
			t.Errorf("ParseSecretKeys(%q) expected error", b)
		}
	}
}

func TestParsePrefixes(t *testing.T) {
	got, err := ParsePrefixes("127.0.0.1/32, ::1/128, 10.1.2.3/8, 185.170.196.188, ::ffff:192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("127.0.0.1/32"),
		netip.MustParsePrefix("::1/128"),
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("185.170.196.188/32"),
		netip.MustParsePrefix("192.0.2.1/32"),
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("prefix %d = %v, want %v", i, got[i], want[i])
		}
	}
	if _, err := ParsePrefixes("10.0.0.0/33"); err == nil {
		t.Error("expected error for invalid CIDR")
	}
	if _, err := ParsePrefixes("not-an-ip"); err == nil {
		t.Error("expected error for invalid address")
	}
	empty, err := ParsePrefixes("")
	if err != nil || len(empty) != 0 {
		t.Errorf("empty = %v, %v", empty, err)
	}
}

func TestParseList(t *testing.T) {
	got := ParseList(" a, ,b ,, c ")
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("ParseList = %v", got)
	}
}
