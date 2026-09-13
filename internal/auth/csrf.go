package auth

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const (
	CSRFHeader = "X-CSRF-Token"
	CSRFField  = "csrf_token"
	nullOrigin = "null"
)

var (
	ErrCrossOrigin = fmt.Errorf("%w: cross-origin request rejected", domain.ErrForbidden)
	ErrCSRF        = fmt.Errorf("%w: invalid csrf token", domain.ErrForbidden)
)

var csrfLabel = []byte("calendar:csrf:v1")

func CSRFToken(secret []byte) string {
	raw := crypto.HMACSHA256(secret, csrfLabel)
	mask := crypto.RandomBytes(len(raw))
	out := make([]byte, 2*len(raw))
	copy(out, mask)
	for i := range raw {
		out[len(raw)+i] = raw[i] ^ mask[i]
	}
	return base64.RawURLEncoding.EncodeToString(out)
}

func VerifyCSRF(secret []byte, token string) bool {
	b, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(b) != 64 {
		return false
	}
	unmasked := make([]byte, 32)
	for i := range unmasked {
		unmasked[i] = b[32+i] ^ b[i]
	}
	return crypto.Equal(unmasked, crypto.HMACSHA256(secret, csrfLabel))
}

func CheckOrigin(r *http.Request, origin string) error {
	if IsSafeMethod(r.Method) {
		return nil
	}
	site := r.Header.Get("Sec-Fetch-Site")
	switch site {
	case "", "same-origin", "none":
	default:
		return ErrCrossOrigin
	}
	o := r.Header.Get("Origin")
	switch {
	case o == "":
		return nil
	case o == nullOrigin:
		// A page served with Referrer-Policy: no-referrer, like a public share,
		// posts with Origin: null even to its own site. Sec-Fetch-Site is set by
		// the browser and cannot be forged by a cross-site caller, so it is the
		// only thing that can still vouch for the request.
		if site != "same-origin" {
			return ErrCrossOrigin
		}
	case !strings.EqualFold(o, origin):
		return ErrCrossOrigin
	}
	return nil
}

func IsSafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}
