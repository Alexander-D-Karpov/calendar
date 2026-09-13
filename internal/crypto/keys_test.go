package crypto

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestNewKeyRingErrors(t *testing.T) {
	good := bytes.Repeat([]byte{1}, KeySize)
	cases := []struct {
		name   string
		keys   map[uint32][]byte
		active uint32
	}{
		{"empty", map[uint32][]byte{}, 0},
		{"zero id", map[uint32][]byte{0: good}, 0},
		{"short key", map[uint32][]byte{1: good[:16]}, 0},
		{"missing active", map[uint32][]byte{1: good}, 2},
	}
	for _, c := range cases {
		if _, err := NewKeyRing(c.keys, c.active); err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}
}

func TestKeyRingIDs(t *testing.T) {
	kr := testRing(t, 0, 3, 1, 2)
	ids := kr.IDs()
	if len(ids) != 3 || ids[0] != 1 || ids[1] != 2 || ids[2] != 3 {
		t.Fatalf("IDs() = %v", ids)
	}
	if kr.Active() != 3 {
		t.Fatalf("Active() = %d", kr.Active())
	}
}

func TestNewKeyRingCopiesKeys(t *testing.T) {
	key := bytes.Repeat([]byte{1}, KeySize)
	kr, err := NewKeyRing(map[uint32][]byte{1: key}, 0)
	if err != nil {
		t.Fatal(err)
	}
	before := kr.Derive("x")
	key[0] = 99
	if !bytes.Equal(before, kr.Derive("x")) {
		t.Fatal("key ring must not alias caller memory")
	}
}

func TestDerive(t *testing.T) {
	kr := testRing(t, 0, 1)
	a := kr.Derive("cookie")
	b := kr.Derive("cookie")
	c := kr.Derive("oauth")
	if len(a) != KeySize {
		t.Fatalf("len = %d", len(a))
	}
	if !bytes.Equal(a, b) {
		t.Fatal("Derive must be deterministic")
	}
	if bytes.Equal(a, c) {
		t.Fatal("different purposes must derive different keys")
	}
	other := testRing(t, 0, 1, 2)
	if bytes.Equal(a, other.Derive("cookie")) {
		t.Fatal("different active keys must derive different keys")
	}
}

func TestDeriveFor(t *testing.T) {
	old := testRing(t, 0, 1)
	both := testRing(t, 0, 1, 2)
	got, err := both.DeriveFor(1, "cookie")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, old.Derive("cookie")) {
		t.Fatal("DeriveFor(1) must match a ring whose active key is 1")
	}
	if _, err := both.DeriveFor(9, "cookie"); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("err = %v, want ErrUnknownKey", err)
	}
}

func TestSignVerifyAcrossRotation(t *testing.T) {
	msg := []byte("oauth-state:abc")
	old := testRing(t, 0, 1)
	sig := old.Sign("oauth", msg)
	if len(sig) != MACSize {
		t.Fatalf("len = %d", len(sig))
	}
	if !old.Verify("oauth", msg, sig) {
		t.Fatal("signature must verify with the same ring")
	}

	both := testRing(t, 0, 1, 2)
	if !both.Verify("oauth", msg, sig) {
		t.Fatal("signature from key 1 must verify after rotation to key 2")
	}
	fresh := both.Sign("oauth", msg)
	if id, _ := KeyID(fresh); id != 2 {
		t.Fatalf("new signature key id = %d, want 2", id)
	}

	newOnly := testRing(t, 0, 2)
	if newOnly.Verify("oauth", msg, sig) {
		t.Fatal("signature from a removed key must not verify")
	}
	if both.Verify("flash", msg, sig) {
		t.Fatal("signature must be bound to its purpose")
	}
	if both.Verify("oauth", []byte("oauth-state:abd"), sig) {
		t.Fatal("signature must be bound to its message")
	}
	if both.Verify("oauth", msg, sig[:MACSize-1]) {
		t.Fatal("truncated signature must not verify")
	}
	tampered := bytes.Clone(sig)
	tampered[len(tampered)-1] ^= 1
	if both.Verify("oauth", msg, tampered) {
		t.Fatal("tampered signature must not verify")
	}
	if bytes.Equal(both.Derive("oauth"), both.Derive("mac:oauth")) {
		t.Fatal("mac keys must be separated from plain derived keys")
	}
}

func TestRandomBase62(t *testing.T) {
	for _, n := range []int{1, 8, 43, 100} {
		s := RandomBase62(n)
		if len(s) != n {
			t.Fatalf("len = %d, want %d", len(s), n)
		}
		for _, r := range s {
			if !strings.ContainsRune(base62Alphabet, r) {
				t.Fatalf("invalid rune %q", r)
			}
		}
	}
	first, second := RandomBase62(43), RandomBase62(43)
	if first == second {
		t.Fatal("random strings collided")
	}
}

func TestRandomToken(t *testing.T) {
	tok := RandomToken(32)
	if len(tok) != 43 {
		t.Fatalf("len = %d", len(tok))
	}
	if strings.ContainsAny(tok, "+/=") {
		t.Fatalf("token must be url safe: %q", tok)
	}
}

func TestHashAndEqual(t *testing.T) {
	a := HashToken("cal_abc_def")
	b := HashToken("cal_abc_def")
	c := HashToken("cal_abc_deg")
	if len(a) != 32 {
		t.Fatalf("len = %d", len(a))
	}
	if !Equal(a, b) || Equal(a, c) {
		t.Fatal("Equal mismatch")
	}
	if Equal(a, a[:31]) {
		t.Fatal("different lengths must not be equal")
	}
	if len(HMACSHA256([]byte("k"), []byte("m"))) != 32 {
		t.Fatal("hmac length")
	}
}
