package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/config"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/ratelimit"
)

const testPassword = "correct horse"

var meta = Meta{IP: netip.MustParseAddr("198.51.100.7"), UserAgent: "test"}

func newService(t *testing.T, mut func(*Options)) (*Service, *fakeRepo, *clock.Fake) {
	t.Helper()
	repo := newFakeRepo()
	clk := clock.NewFake(t0)
	o := Options{
		Repo:              repo,
		Hasher:            testHasher(t, testParams, 4),
		Clock:             clk,
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		Policy:            SessionPolicy{TTL: time.Hour, AbsoluteTTL: 24 * time.Hour, Secure: true},
		Registration:      config.Registration{Enabled: true, PasswordEnabled: true},
		PasswordMinLength: 10,
		LoginRate:         config.Rate{Count: 20, Per: 15 * time.Minute},
		DefaultTimezone:   "UTC",
	}
	if mut != nil {
		mut(&o)
	}
	return NewService(o), repo, clk
}

func register(t *testing.T, s *Service, email string) domain.User {
	t.Helper()
	u, err := s.Register(context.Background(), RegisterInput{Email: email, Password: testPassword, DisplayName: "Alice"}, meta)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func login(t *testing.T, s *Service, email string) *Principal {
	t.Helper()
	iss, err := s.Login(context.Background(), email, testPassword, meta)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Authenticate(context.Background(), iss.Token)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func fieldsOf(t *testing.T, err error) []string {
	t.Helper()
	var ve *domain.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected validation error, got %v", err)
	}
	out := make([]string, len(ve.Fields))
	for i, f := range ve.Fields {
		out[i] = f.Field
	}
	return out
}

func flip(b byte) string {
	if b == 'a' {
		return "b"
	}
	return "a"
}

func TestNormalizeEmail(t *testing.T) {
	cases := map[string]string{
		" Alice@Example.COM ":   "Alice@example.com",
		"a.b+c@sub.example.org": "a.b+c@sub.example.org",
		"":                      "",
		"plain":                 "",
		"a@b":                   "",
		"a@b@c.com":             "",
		"Alice <a@b.com>":       "",
		"<a@b.com>":             "",
		"a@.com":                "",
		"a@com.":                "",
		"@example.com":          "",
	}
	for in, want := range cases {
		got, err := NormalizeEmail(in)
		if want == "" {
			if err == nil {
				t.Errorf("NormalizeEmail(%q) = %q, want error", in, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("NormalizeEmail(%q) = %q %v, want %q", in, got, err, want)
		}
	}
}

func TestRegister(t *testing.T) {
	ctx := context.Background()
	s, repo, _ := newService(t, nil)
	u := register(t, s, "Alice@Example.COM")
	if u.Email != "Alice@example.com" || !u.Verified() || u.Timezone != "UTC" || !strings.HasPrefix(u.PasswordHash, "$argon2id$") {
		t.Fatalf("user = %+v", u)
	}
	_, err := s.Register(ctx, RegisterInput{Email: "alice@EXAMPLE.com", Password: testPassword}, meta)
	if got := fieldsOf(t, err); !slices.Equal(got, []string{"email"}) {
		t.Fatalf("duplicate fields = %v", got)
	}
	_, err = s.Register(ctx, RegisterInput{Email: "not-an-email", Password: "short", Timezone: "Mars/Base"}, meta)
	if got := fieldsOf(t, err); !slices.Equal(got, []string{"email", "password", "timezone"}) {
		t.Fatalf("invalid fields = %v", got)
	}
	if got := repo.actions(); !slices.Equal(got, []string{"user.register"}) {
		t.Fatalf("audit = %v", got)
	}
}

func TestRegisterPolicy(t *testing.T) {
	ctx := context.Background()
	closed, _, _ := newService(t, func(o *Options) { o.Registration.Enabled = false })
	if _, err := closed.Register(ctx, RegisterInput{Email: "a@example.com", Password: testPassword}, meta); !errors.Is(err, ErrRegistrationClosed) {
		t.Fatalf("closed err = %v", err)
	}

	s, _, _ := newService(t, func(o *Options) {
		o.Registration.AllowedDomains = []string{"akarpov.ru"}
		o.VerifyEmail = true
	})
	_, err := s.Register(ctx, RegisterInput{Email: "x@evil.test", Password: testPassword}, meta)
	if got := fieldsOf(t, err); !slices.Equal(got, []string{"email"}) {
		t.Fatalf("restricted fields = %v", got)
	}
	u, err := s.Register(ctx, RegisterInput{Email: "sasha@akarpov.ru", Password: testPassword}, meta)
	if err != nil || u.Verified() {
		t.Fatalf("verify-mode user = %+v %v", u, err)
	}
	admin, err := s.CreateUser(ctx, CreateUserInput{RegisterInput: RegisterInput{Email: "ops@elsewhere.test", Password: testPassword}, Verified: true}, Meta{})
	if err != nil || !admin.Verified() {
		t.Fatalf("admin user = %+v %v", admin, err)
	}
}

func TestLogin(t *testing.T) {
	ctx := context.Background()
	s, repo, _ := newService(t, nil)
	u := register(t, s, "alice@example.com")

	iss, err := s.Login(ctx, " ALICE@example.com ", testPassword, meta)
	if err != nil {
		t.Fatal(err)
	}
	if iss.User.ID != u.ID || iss.Session.ReauthAt == nil || len(iss.Session.CSRFSecret) != 32 || iss.Session.IP != meta.IP {
		t.Fatalf("issued = %+v", iss.Session)
	}
	p, err := s.Authenticate(ctx, iss.Token)
	if err != nil || p.UserID != u.ID || !p.Fresh(t0) || p.Refreshed {
		t.Fatalf("principal = %+v %v", p, err)
	}

	bad := []struct{ email, pw string }{
		{"alice@example.com", "wrong password"},
		{"nobody@example.com", testPassword},
		{"", testPassword},
		{"alice@example.com", strings.Repeat("a", MaxPasswordBytes+1)},
	}
	for _, c := range bad {
		if _, err := s.Login(ctx, c.email, c.pw, meta); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("Login(%q) err = %v", c.email, err)
		}
	}
	actions := repo.actions()
	if !slices.Contains(actions, "login") || !slices.Contains(actions, "login.failed") {
		t.Fatalf("audit = %v", actions)
	}
}

func TestLoginRateLimit(t *testing.T) {
	ctx := context.Background()
	s, _, clk := newService(t, func(o *Options) { o.LoginRate = config.Rate{Count: 3, Per: 15 * time.Minute} })
	register(t, s, "alice@example.com")
	for range 3 {
		if _, err := s.Login(ctx, "alice@example.com", "wrong password", meta); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("err = %v", err)
		}
	}
	_, err := s.Login(ctx, "alice@example.com", testPassword, meta)
	var rl *ratelimit.Error
	if !errors.As(err, &rl) || !errors.Is(err, domain.ErrRateLimited) || rl.RetryAfter != 5*time.Minute {
		t.Fatalf("limited err = %v", err)
	}
	clk.Advance(5 * time.Minute)
	if _, err := s.Login(ctx, "alice@example.com", testPassword, meta); err != nil {
		t.Fatalf("after wait: %v", err)
	}
}

func TestLoginDisabledAndRehash(t *testing.T) {
	ctx := context.Background()
	s, repo, _ := newService(t, nil)
	u := register(t, s, "alice@example.com")

	if err := s.SetDisabled(ctx, u.ID, true, Meta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Login(ctx, "alice@example.com", testPassword, meta); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled err = %v", err)
	}
	if _, err := s.Login(ctx, "alice@example.com", "wrong password", meta); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled wrong pw err = %v", err)
	}
	if err := s.SetDisabled(ctx, u.ID, false, Meta{}); err != nil {
		t.Fatal(err)
	}

	stronger, _, _ := newService(t, func(o *Options) {
		o.Repo = repo
		o.Hasher = testHasher(t, Params{Memory: 2048, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}, 1)
	})
	if _, err := stronger.Login(ctx, "alice@example.com", testPassword, meta); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.UserByID(ctx, u.ID)
	if !strings.Contains(got.PasswordHash, "m=2048") {
		t.Fatalf("hash not upgraded: %s", got.PasswordHash)
	}
}

func TestAuthenticateLifecycle(t *testing.T) {
	ctx := context.Background()
	s, repo, clk := newService(t, nil)
	register(t, s, "alice@example.com")
	iss, err := s.Login(ctx, "alice@example.com", testPassword, meta)
	if err != nil {
		t.Fatal(err)
	}

	clk.Advance(30 * time.Second)
	if p, err := s.Authenticate(ctx, iss.Token); err != nil || p.Refreshed {
		t.Fatalf("early touch = %+v %v", p, err)
	}
	clk.Advance(2 * time.Minute)
	p, err := s.Authenticate(ctx, iss.Token)
	if err != nil || !p.Refreshed || !p.ExpiresAt.Equal(clk.Now().Add(time.Hour)) {
		t.Fatalf("touch = %+v %v", p, err)
	}
	clk.Advance(10 * time.Minute)
	if p.Fresh(clk.Now()) {
		t.Fatal("sudo window must expire")
	}
	clk.Advance(61 * time.Minute)
	if _, err := s.Authenticate(ctx, iss.Token); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expired err = %v", err)
	}
	if len(repo.sessions) != 0 {
		t.Fatal("expired session must be deleted")
	}
	if _, err := s.Authenticate(ctx, "unknown"); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("unknown err = %v", err)
	}
}

func TestReauthAndSessions(t *testing.T) {
	ctx := context.Background()
	s, _, clk := newService(t, nil)
	register(t, s, "alice@example.com")
	first, err := s.Login(ctx, "alice@example.com", testPassword, meta)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Login(ctx, "alice@example.com", testPassword, meta)
	if err != nil {
		t.Fatal(err)
	}

	clk.Advance(11 * time.Minute)
	p, err := s.Authenticate(ctx, first.Token)
	if err != nil || p.Fresh(clk.Now()) {
		t.Fatalf("stale principal = %+v %v", p, err)
	}
	if err := s.Reauth(ctx, p, "wrong password", meta); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("bad reauth err = %v", err)
	}
	if err := s.Reauth(ctx, p, testPassword, meta); err != nil || !p.Fresh(clk.Now()) {
		t.Fatalf("reauth = %v fresh=%v", err, p.Fresh(clk.Now()))
	}
	if again, _ := s.Authenticate(ctx, first.Token); !again.Fresh(clk.Now()) {
		t.Fatal("reauth must persist")
	}

	bob := register(t, s, "bob@example.com")
	if err := s.RevokeSession(ctx, bob.ID, second.Session.ID, meta); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign revoke err = %v", err)
	}
	if n, err := s.RevokeOtherSessions(ctx, p, meta); err != nil || n != 1 {
		t.Fatalf("RevokeOtherSessions = %d %v", n, err)
	}
	if _, err := s.Authenticate(ctx, second.Token); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("revoked session err = %v", err)
	}
	if err := s.Logout(ctx, p, meta); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, first.Token); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("logged out err = %v", err)
	}
}

func TestAPITokens(t *testing.T) {
	ctx := context.Background()
	s, _, clk := newService(t, nil)
	u := register(t, s, "alice@example.com")
	p := login(t, s, "alice@example.com")

	raw, tok, err := s.CreateAPIToken(ctx, p, TokenInput{Name: " cli ", Scopes: []string{"todos:write", "calendars:read"}}, meta)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Name != "cli" || !slices.Equal(tok.Scopes, []string{"calendars:read", "todos:write"}) {
		t.Fatalf("token = %+v", tok)
	}

	ap, err := s.AuthenticateAPIToken(ctx, raw, meta)
	if err != nil || ap.UserID != u.ID || ap.TokenID != tok.ID || ap.IsSession() {
		t.Fatalf("api principal = %+v %v", ap, err)
	}
	if !ap.Can(ScopeTodosRead) || !ap.Can(ScopeCalendarsRead) || ap.Can(ScopeCalendarsWrite) {
		t.Fatal("scope checks mismatch")
	}
	if _, _, err := s.CreateAPIToken(ctx, ap, TokenInput{Name: "x", Scopes: []string{"todos:read"}}, meta); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("token-created token err = %v", err)
	}

	tampered := raw[:len(raw)-1] + flip(raw[len(raw)-1])
	for _, bad := range []string{tampered, "cal_short", ""} {
		if _, err := s.AuthenticateAPIToken(ctx, bad, meta); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("AuthenticateAPIToken(%q) err = %v", bad, err)
		}
	}

	past := t0.Add(-time.Hour)
	_, _, err = s.CreateAPIToken(ctx, p, TokenInput{Name: " ", Scopes: []string{"admin"}, ExpiresAt: &past}, meta)
	if got := fieldsOf(t, err); !slices.Equal(got, []string{"name", "scopes", "expires_at"}) {
		t.Fatalf("invalid token fields = %v", got)
	}

	exp := t0.Add(time.Hour)
	short, _, err := s.CreateAPIToken(ctx, p, TokenInput{Name: "short", Scopes: []string{"todos:read"}, ExpiresAt: &exp}, meta)
	if err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListAPITokens(ctx, u.ID); len(list) != 2 {
		t.Fatalf("tokens = %d", len(list))
	}

	bob := register(t, s, "bob@example.com")
	if err := s.RevokeAPIToken(ctx, bob.ID, tok.ID, meta); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign revoke err = %v", err)
	}
	if err := s.RevokeAPIToken(ctx, u.ID, tok.ID, meta); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateAPIToken(ctx, raw, meta); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("revoked err = %v", err)
	}

	clk.Advance(2 * time.Hour)
	if _, err := s.AuthenticateAPIToken(ctx, short, meta); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired err = %v", err)
	}
	if _, _, err := s.CreateAPIToken(ctx, p, TokenInput{Name: "late", Scopes: []string{"todos:read"}}, meta); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("stale session err = %v", err)
	}
}
