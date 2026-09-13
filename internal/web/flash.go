package web

import (
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
)

const (
	flashCookie = "flash"
	flashMaxAge = 60
)

type Flash struct {
	Kind string
	Text string
	Undo string
}

type flasher struct {
	keys   *crypto.KeyRing
	secure bool
}

func (f flasher) cookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     flashCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   f.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (f flasher) Set(w http.ResponseWriter, kind, text, undo string) {
	payload := []byte(kind + "\x00" + text + "\x00" + undo)
	sig := f.keys.Sign("flash", payload)
	f.cookie(w, base64.RawURLEncoding.EncodeToString(payload)+"."+base64.RawURLEncoding.EncodeToString(sig), flashMaxAge)
}

func (f flasher) Pop(w http.ResponseWriter, r *http.Request) *Flash {
	c, err := r.Cookie(flashCookie)
	if err != nil {
		return nil
	}
	f.cookie(w, "", -1)
	enc, encSig, ok := strings.Cut(c.Value, ".")
	if !ok {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return nil
	}
	sig, err := base64.RawURLEncoding.DecodeString(encSig)
	if err != nil || !f.keys.Verify("flash", payload, sig) {
		return nil
	}
	parts := strings.SplitN(string(payload), "\x00", 3)
	if len(parts) < 2 {
		return nil
	}
	out := &Flash{Kind: parts[0], Text: parts[1]}
	if len(parts) == 3 {
		out.Undo = parts[2]
	}
	switch out.Kind {
	case "ok", "error", "info":
		return out
	}
	return nil
}
