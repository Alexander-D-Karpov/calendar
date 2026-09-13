package service

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

func TestShareTokens(t *testing.T) {
	keys, err := crypto.NewKeyRing(map[uint32][]byte{1: bytes.Repeat([]byte{1}, crypto.KeySize)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := NewShares(nil, nil, keys, nil)
	sh := domain.Share{ID: domain.NewID()}
	s.issue(&sh)
	if !ValidShareToken(sh.Token) || !crypto.Equal(sh.TokenHash, crypto.HashToken(sh.Token)) {
		t.Fatalf("token = %q", sh.Token)
	}
	tok := sh.Token
	sh.Token = ""
	s.reveal(&sh)
	if sh.Token != tok {
		t.Fatal("reveal must decrypt the stored token")
	}
	moved := sh
	moved.ID, moved.Token = domain.NewID(), ""
	s.reveal(&moved)
	if moved.Token != "" {
		t.Fatal("token must be bound to its share id")
	}
	for _, bad := range []string{"", tok[:42], tok + "a", strings.Repeat("!", 43), strings.Repeat("a", 42) + "="} {
		if ValidShareToken(bad) {
			t.Errorf("ValidShareToken(%q) accepted", bad)
		}
	}
}
