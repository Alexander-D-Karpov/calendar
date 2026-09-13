package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

var testParams = Params{Memory: 1024, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}

func testHasher(t *testing.T, p Params, concurrency int) *Hasher {
	t.Helper()
	h, err := NewHasher(p, concurrency)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHashVerify(t *testing.T) {
	ctx := context.Background()
	h := testHasher(t, testParams, 2)
	enc, err := h.Hash(ctx, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "$argon2id$v=19$m=1024,t=1,p=1$") {
		t.Fatalf("encoded = %q", enc)
	}
	if again, _ := h.Hash(ctx, "correct horse battery"); again == enc {
		t.Fatal("hashes must be salted")
	}

	ok, rehash, err := h.Verify(ctx, "correct horse battery", enc)
	if !ok || rehash || err != nil {
		t.Fatalf("Verify = %v %v %v", ok, rehash, err)
	}
	if ok, _, err := h.Verify(ctx, "correct horse batterY", enc); ok || err != nil {
		t.Fatalf("wrong password = %v %v", ok, err)
	}

	stronger := testHasher(t, Params{Memory: 2048, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}, 1)
	if ok, rehash, err := stronger.Verify(ctx, "correct horse battery", enc); !ok || !rehash || err != nil {
		t.Fatalf("upgrade Verify = %v %v %v", ok, rehash, err)
	}
	h.Dummy(ctx, "anything")
}

func TestVerifyMalformed(t *testing.T) {
	ctx := context.Background()
	h := testHasher(t, testParams, 1)
	enc, _ := h.Hash(ctx, "password123")
	parts := strings.Split(enc, "$")
	with := func(i int, v string) string {
		p := append([]string(nil), parts...)
		p[i] = v
		return strings.Join(p, "$")
	}
	bad := []string{
		"",
		"plain",
		with(1, "argon2i"),
		with(2, "v=16"),
		with(3, "m=1024,t=1,p=1x"),
		with(3, "m=01024,t=1,p=1"),
		with(3, "m=4194304,t=1,p=1"),
		with(3, "m=1024,t=0,p=1"),
		with(3, "m=1024,t=1,p=0"),
		with(4, "!!"),
		with(4, "c2FsdA"),
		with(5, "c2hvcnQ"),
		enc + "$extra",
	}
	for _, b := range bad {
		if _, _, err := h.Verify(ctx, "password123", b); !errors.Is(err, ErrMalformedHash) {
			t.Errorf("Verify(%q) err = %v", b, err)
		}
	}
}

func TestHasherRespectsContext(t *testing.T) {
	h := testHasher(t, testParams, 1)
	h.sem <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.Hash(ctx, "password123"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestNewHasherRejectsWeakParams(t *testing.T) {
	if _, err := NewHasher(Params{Memory: 1024, Time: 0, Threads: 1, SaltLen: 16, KeyLen: 32}, 1); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidatePassword(t *testing.T) {
	cases := map[string]bool{
		"0123456789":                 true,
		"пароль1234":                 true,
		"short":                      false,
		"          ":                 false,
		"\xff\xfe0123456789":         false,
		strings.Repeat("a", 1025):    false,
		strings.Repeat("a", 1024):    true,
		strings.Repeat("я", 9) + "1": true,
	}
	for pw, ok := range cases {
		err := ValidatePassword(pw, 10)
		if ok != (err == nil) {
			t.Errorf("ValidatePassword(%q) = %v", pw, err)
		}
		if err != nil && !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("error must wrap ErrInvalid: %v", err)
		}
	}
}
