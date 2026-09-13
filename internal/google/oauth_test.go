package google

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestAuthURL(t *testing.T) {
	o := NewOAuth("cid", "secret", "https://cal.test"+CallbackPath, http.DefaultClient)
	v := NewVerifier()
	raw := o.AuthURL(AuthOptions{
		State: "st", Nonce: "nn", Verifier: v, LoginHint: "a@b.test", SelectAccount: true,
		Scopes: []string{"https://www.googleapis.com/auth/calendar"}, Offline: true,
	})
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	want := map[string]string{
		"client_id":              "cid",
		"state":                  "st",
		"nonce":                  "nn",
		"redirect_uri":           "https://cal.test/auth/google/callback",
		"code_challenge_method":  "S256",
		"login_hint":             "a@b.test",
		"prompt":                 "consent select_account",
		"access_type":            "offline",
		"include_granted_scopes": "true",
		"response_type":          "code",
	}
	for k, w := range want {
		if q.Get(k) != w {
			t.Errorf("%s = %q, want %q", k, q.Get(k), w)
		}
	}
	if q.Get("code_challenge") == "" || strings.Contains(raw, v) {
		t.Fatal("challenge must be sent and the verifier must stay secret")
	}
	if s := q.Get("scope"); !strings.Contains(s, "openid") || !strings.Contains(s, "auth/calendar") {
		t.Fatalf("scope = %q", s)
	}
	plain, _ := url.Parse(o.AuthURL(AuthOptions{State: "s", Nonce: "n", Verifier: v}))
	if plain.Query().Get("prompt") != "" || plain.Query().Get("access_type") != "" || plain.Query().Get("scope") != "openid email profile" {
		t.Fatalf("plain = %v", plain.Query())
	}
}

func TestFlexBool(t *testing.T) {
	for in, want := range map[string]bool{`true`: true, `"true"`: true, `false`: false, `"false"`: false} {
		var b flexBool
		if err := json.Unmarshal([]byte(in), &b); err != nil || bool(b) != want {
			t.Errorf("%s = %v %v", in, b, err)
		}
	}
	var b flexBool
	if err := json.Unmarshal([]byte(`"yes"`), &b); err == nil {
		t.Fatal("invalid boolean accepted")
	}
}
