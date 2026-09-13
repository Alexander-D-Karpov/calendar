package auth

import (
	"net/http"
	"strings"

	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
)

const (
	tokenPrefix = "cal_"
	PrefixLen   = 12
	SecretLen   = 43
)

type APIToken struct {
	Token  string
	Prefix string
	Hash   []byte
}

func NewAPIToken() APIToken {
	prefix := crypto.RandomBase62(PrefixLen)
	token := tokenPrefix + prefix + "_" + crypto.RandomBase62(SecretLen)
	return APIToken{Token: token, Prefix: prefix, Hash: crypto.HashToken(token)}
}

func ParseAPIToken(s string) (string, bool) {
	rest, ok := strings.CutPrefix(s, tokenPrefix)
	if !ok {
		return "", false
	}
	prefix, secret, ok := strings.Cut(rest, "_")
	if !ok || len(prefix) != PrefixLen || len(secret) != SecretLen || !isBase62(prefix) || !isBase62(secret) {
		return "", false
	}
	return prefix, true
}

func VerifyAPIToken(token string, hash []byte) bool {
	return crypto.Equal(crypto.HashToken(token), hash)
}

func BearerToken(r *http.Request) (string, bool) {
	scheme, tok, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	tok = strings.TrimSpace(tok)
	return tok, tok != ""
}

func isBase62(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z') {
			return false
		}
	}
	return true
}
