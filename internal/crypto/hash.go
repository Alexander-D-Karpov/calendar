package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
)

func SHA256(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

func HashToken(token string) []byte {
	return SHA256([]byte(token))
}

func Equal(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

func HMACSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}
