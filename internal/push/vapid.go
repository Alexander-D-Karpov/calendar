package push

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"
)

const jwtTTL = 12 * time.Hour

var b64 = base64.RawURLEncoding

type vapidClaims struct {
	Aud string `json:"aud"`
	Exp int64  `json:"exp"`
	Sub string `json:"sub"`
}

func decodeKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("push: key is not base64")
}

func parseKeys(pub, priv string) ([]byte, *ecdsa.PrivateKey, error) {
	p, err := decodeKey(pub)
	if err != nil {
		return nil, nil, err
	}
	if len(p) != 65 || p[0] != 4 {
		return nil, nil, errors.New("push: WEBPUSH_VAPID_PUBLIC must be an uncompressed P-256 point")
	}
	d, err := decodeKey(priv)
	if err != nil {
		return nil, nil, err
	}
	if len(d) != 32 {
		return nil, nil, errors.New("push: WEBPUSH_VAPID_PRIVATE must be 32 bytes")
	}
	ek, err := ecdh.P256().NewPrivateKey(d)
	if err != nil {
		return nil, nil, fmt.Errorf("push: private key: %w", err)
	}
	if !bytes.Equal(ek.PublicKey().Bytes(), p) {
		return nil, nil, errors.New("push: VAPID public and private keys do not match")
	}
	key := &ecdsa.PrivateKey{
		PublicKey: ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     new(big.Int).SetBytes(p[1:33]),
			Y:     new(big.Int).SetBytes(p[33:65]),
		},
		D: new(big.Int).SetBytes(d),
	}
	return p, key, nil
}

func (s *Sender) authorization(endpoint string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return "", errors.New("push: invalid endpoint")
	}
	claims, err := json.Marshal(vapidClaims{
		Aud: u.Scheme + "://" + u.Host,
		Exp: now.Add(jwtTTL).Unix(),
		Sub: s.subject,
	})
	if err != nil {
		return "", err
	}
	signing := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`)) + "." + b64.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	r, ss, err := ecdsa.Sign(rand.Reader, s.priv, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	ss.FillBytes(sig[32:])
	return "vapid t=" + signing + "." + b64.EncodeToString(sig) + ", k=" + b64.EncodeToString(s.pub), nil
}

// ecdsaKey lets push.go hold the signing key without importing crypto itself.
type ecdsaKey = ecdsa.PrivateKey
