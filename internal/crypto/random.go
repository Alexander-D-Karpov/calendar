package crypto

import (
	"crypto/rand"
	"encoding/base64"
)

const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func RandomBytes(n int) []byte {
	b := make([]byte, n)
	fill(b)
	return b
}

func RandomToken(n int) string {
	return base64.RawURLEncoding.EncodeToString(RandomBytes(n))
}

func RandomBase62(n int) string {
	out := make([]byte, 0, n)
	buf := make([]byte, n+n/4+8)
	for len(out) < n {
		fill(buf)
		for _, b := range buf {
			if b >= 248 {
				continue
			}
			out = append(out, base62Alphabet[b%62])
			if len(out) == n {
				break
			}
		}
	}
	return string(out)
}

func fill(b []byte) {
	if _, err := rand.Read(b); err != nil {
		panic("crypto: random source failed: " + err.Error())
	}
}
