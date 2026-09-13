package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIToken(t *testing.T) {
	tok := NewAPIToken()
	if !strings.HasPrefix(tok.Token, "cal_"+tok.Prefix+"_") || len(tok.Token) != len("cal_")+PrefixLen+1+SecretLen {
		t.Fatalf("token = %q", tok.Token)
	}
	if prefix, ok := ParseAPIToken(tok.Token); !ok || prefix != tok.Prefix {
		t.Fatalf("ParseAPIToken = %q %v", prefix, ok)
	}
	if !VerifyAPIToken(tok.Token, tok.Hash) || VerifyAPIToken(tok.Token+"x", tok.Hash) {
		t.Fatal("VerifyAPIToken mismatch")
	}
	other := NewAPIToken()
	if other.Prefix == tok.Prefix || VerifyAPIToken(other.Token, tok.Hash) {
		t.Fatal("tokens must be unique")
	}
}

func TestParseAPITokenRejects(t *testing.T) {
	tok := NewAPIToken().Token
	bad := []string{
		"",
		"cal_",
		tok[:len(tok)-1],
		tok + "a",
		strings.Replace(tok, "cal_", "cak_", 1),
		strings.Replace(tok, "_", "-", 2),
		"cal_" + strings.Repeat("a", PrefixLen) + "_" + strings.Repeat("!", SecretLen),
		"cal_" + strings.Repeat("a", PrefixLen+1) + "_" + strings.Repeat("b", SecretLen-1),
	}
	for _, b := range bad {
		if _, ok := ParseAPIToken(b); ok {
			t.Errorf("ParseAPIToken(%q) accepted", b)
		}
	}
}

func TestBearerToken(t *testing.T) {
	cases := map[string]string{
		"Bearer abc":   "abc",
		"bearer  abc ": "abc",
		"Basic abc":    "",
		"Bearer ":      "",
		"":             "",
		"Bearerabc":    "",
	}
	for header, want := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		got, ok := BearerToken(req)
		if got != want || ok != (want != "") {
			t.Errorf("BearerToken(%q) = %q %v", header, got, ok)
		}
	}
}
