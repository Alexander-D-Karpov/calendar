package crypto

import (
	"bytes"
	"errors"
	"testing"
)

func testRing(t *testing.T, active uint32, ids ...uint32) *KeyRing {
	t.Helper()
	keys := make(map[uint32][]byte, len(ids))
	for _, id := range ids {
		keys[id] = bytes.Repeat([]byte{byte(id)}, KeySize)
	}
	kr, err := NewKeyRing(keys, active)
	if err != nil {
		t.Fatal(err)
	}
	return kr
}

func TestSealOpen(t *testing.T) {
	kr := testRing(t, 0, 1)
	aad := AAD("share_token", "abc")
	ct1 := kr.Seal([]byte("hello"), aad)
	ct2 := kr.Seal([]byte("hello"), aad)
	if bytes.Equal(ct1, ct2) {
		t.Fatal("ciphertexts must differ for the same plaintext")
	}
	pt, err := kr.Open(ct1, aad)
	if err != nil {
		t.Fatal(err)
	}
	if string(pt) != "hello" {
		t.Fatalf("got %q", pt)
	}
}

func TestOpenWrongAAD(t *testing.T) {
	kr := testRing(t, 0, 1)
	ct := kr.Seal([]byte("secret"), AAD("share_token", "a"))
	if _, err := kr.Open(ct, AAD("share_token", "b")); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("err = %v, want ErrDecrypt", err)
	}
}

func TestOpenTampered(t *testing.T) {
	kr := testRing(t, 0, 1)
	ct := kr.Seal([]byte("secret"), nil)
	ct[len(ct)-1] ^= 0xff
	if _, err := kr.Open(ct, nil); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("err = %v, want ErrDecrypt", err)
	}
}

func TestOpenMalformed(t *testing.T) {
	kr := testRing(t, 0, 1)
	if _, err := kr.Open([]byte{0, 0}, nil); !errors.Is(err, ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed", err)
	}
	if _, err := kr.Open([]byte{0, 0, 0, 1, 9, 9}, nil); !errors.Is(err, ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed", err)
	}
}

func TestRotation(t *testing.T) {
	aad := AAD("refresh_token", "user-1")
	old := testRing(t, 0, 1)
	ct := old.Seal([]byte("token"), aad)

	both := testRing(t, 0, 1, 2)
	if both.Active() != 2 {
		t.Fatalf("Active() = %d", both.Active())
	}
	if !both.NeedsRotation(ct) {
		t.Fatal("expected NeedsRotation")
	}
	pt, err := both.Open(ct, aad)
	if err != nil || string(pt) != "token" {
		t.Fatalf("open old ciphertext: %q, %v", pt, err)
	}
	re, err := both.Reseal(ct, aad)
	if err != nil {
		t.Fatal(err)
	}
	if id, _ := KeyID(re); id != 2 {
		t.Fatalf("resealed key id = %d", id)
	}
	if both.NeedsRotation(re) {
		t.Fatal("resealed ciphertext should not need rotation")
	}

	newOnly := testRing(t, 0, 2)
	if _, err := newOnly.Open(ct, aad); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("err = %v, want ErrUnknownKey", err)
	}
}

func TestStringHelpers(t *testing.T) {
	kr := testRing(t, 0, 5)
	ct := kr.SealString("https://example.com/feed.ics?token=x", AAD("subscription_url", "id"))
	s, err := kr.OpenString(ct, AAD("subscription_url", "id"))
	if err != nil || s != "https://example.com/feed.ics?token=x" {
		t.Fatalf("OpenString = %q, %v", s, err)
	}
}

func TestAADSeparatesParts(t *testing.T) {
	if bytes.Equal(AAD("ab", "c"), AAD("a", "bc")) {
		t.Fatal("AAD must separate parts")
	}
}
