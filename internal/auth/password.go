package auth

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const MaxPasswordBytes = 1024

var ErrMalformedHash = errors.New("auth: malformed password hash")

var b64 = base64.RawStdEncoding

type Params struct {
	Memory  uint32
	Time    uint32
	Threads uint8
	SaltLen uint32
	KeyLen  uint32
}

var DefaultParams = Params{Memory: 64 * 1024, Time: 3, Threads: 2, SaltLen: 16, KeyLen: 32}

func (p Params) check() error {
	switch {
	case p.Time < 1 || p.Time > 16,
		p.Threads < 1 || p.Threads > 16,
		p.Memory < 8*uint32(p.Threads) || p.Memory > 1<<20,
		p.SaltLen < 8 || p.SaltLen > 64,
		p.KeyLen < 16 || p.KeyLen > 64:
		return ErrMalformedHash
	}
	return nil
}

type Hasher struct {
	params Params
	sem    chan struct{}
	dummy  string
}

func NewHasher(p Params, concurrency int) (*Hasher, error) {
	if err := p.check(); err != nil {
		return nil, fmt.Errorf("auth: invalid argon2 parameters %+v", p)
	}
	if concurrency <= 0 {
		concurrency = max(2, runtime.GOMAXPROCS(0))
	}
	return &Hasher{
		params: p,
		sem:    make(chan struct{}, concurrency),
		dummy:  encode(p, crypto.RandomBytes(int(p.SaltLen)), crypto.RandomBytes(int(p.KeyLen))),
	}, nil
}

func (h *Hasher) Hash(ctx context.Context, password string) (string, error) {
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()
	salt := crypto.RandomBytes(int(h.params.SaltLen))
	key := argon2.IDKey([]byte(password), salt, h.params.Time, h.params.Memory, h.params.Threads, h.params.KeyLen)
	return encode(h.params, salt, key), nil
}

func (h *Hasher) Verify(ctx context.Context, password, encoded string) (ok, rehash bool, err error) {
	p, salt, key, err := decode(encoded)
	if err != nil {
		return false, false, err
	}
	if err := h.acquire(ctx); err != nil {
		return false, false, err
	}
	defer h.release()
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	if subtle.ConstantTimeCompare(got, key) != 1 {
		return false, false, nil
	}
	return true, p != h.params, nil
}

func (h *Hasher) Dummy(ctx context.Context, password string) {
	_, _, _ = h.Verify(ctx, password, h.dummy)
}

func (h *Hasher) acquire(ctx context.Context) error {
	select {
	case h.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Hasher) release() {
	<-h.sem
}

func encode(p Params, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads, b64.EncodeToString(salt), b64.EncodeToString(key))
}

func decode(s string) (Params, []byte, []byte, error) {
	var p Params
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return p, nil, nil, ErrMalformedHash
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil {
		return p, nil, nil, ErrMalformedHash
	}
	if parts[3] != fmt.Sprintf("m=%d,t=%d,p=%d", p.Memory, p.Time, p.Threads) {
		return p, nil, nil, ErrMalformedHash
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return p, nil, nil, ErrMalformedHash
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil {
		return p, nil, nil, ErrMalformedHash
	}
	p.SaltLen, p.KeyLen = uint32(len(salt)), uint32(len(key))
	if err := p.check(); err != nil {
		return p, nil, nil, err
	}
	return p, salt, key, nil
}

func ValidatePassword(password string, minLen int) error {
	var v domain.ValidationError
	switch {
	case !utf8.ValidString(password):
		v.Add("password", "must be valid UTF-8")
	case len(password) > MaxPasswordBytes:
		v.Addf("password", "must be at most %d bytes", MaxPasswordBytes)
	case strings.TrimSpace(password) == "":
		v.Add("password", "must not be blank")
	case utf8.RuneCountInString(password) < minLen:
		v.Addf("password", "must be at least %d characters", minLen)
	}
	return v.Err()
}
