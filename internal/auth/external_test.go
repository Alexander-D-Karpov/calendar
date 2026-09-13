package auth

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

func googleUser(sub, email string) External {
	return External{Provider: domain.ProviderGoogle, Subject: sub, Email: email, EmailVerified: true, Name: "Sasha", Timezone: "Europe/Vilnius"}
}

func withGoogle(o *Options) {
	o.Registration.GoogleEnabled = true
}

func TestExternalSignupAndLogin(t *testing.T) {
	ctx := context.Background()
	s, repo, _ := newService(t, withGoogle)
	iss, err := s.ExternalLogin(ctx, googleUser("g1", "Sasha@Example.COM"), meta)
	if err != nil {
		t.Fatal(err)
	}
	u := iss.User
	if u.HasPassword() || !u.Verified() || u.Timezone != "Europe/Vilnius" || u.Email != "Sasha@example.com" || u.DisplayName != "Sasha" {
		t.Fatalf("user = %+v", u)
	}
	again, err := s.ExternalLogin(ctx, googleUser("g1", "renamed@example.com"), meta)
	if err != nil || again.User.ID != u.ID {
		t.Fatalf("second login = %+v %v", again, err)
	}
	if got := repo.actions(); !slices.Contains(got, "user.register") || !slices.Contains(got, "login") {
		t.Fatalf("audit = %v", got)
	}
	unverified := googleUser("g2", "x@example.com")
	unverified.EmailVerified = false
	if _, err := s.ExternalLogin(ctx, unverified, meta); !errors.Is(err, ErrEmailUnverified) {
		t.Fatalf("unverified err = %v", err)
	}
	if err := s.SetDisabled(ctx, u.ID, true, Meta{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExternalLogin(ctx, googleUser("g1", "x@example.com"), meta); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled err = %v", err)
	}
}

func TestExternalSignupPolicy(t *testing.T) {
	ctx := context.Background()
	closed, _, _ := newService(t, nil)
	if _, err := closed.ExternalLogin(ctx, googleUser("g1", "a@example.com"), meta); !errors.Is(err, ErrRegistrationClosed) {
		t.Fatalf("closed err = %v", err)
	}
	restricted, _, _ := newService(t, func(o *Options) {
		withGoogle(o)
		o.Registration.AllowedDomains = []string{"akarpov.ru"}
	})
	if _, err := restricted.ExternalLogin(ctx, googleUser("g1", "a@example.com"), meta); !errors.Is(err, ErrDomainNotAllowed) {
		t.Fatalf("domain err = %v", err)
	}
	if _, err := restricted.ExternalLogin(ctx, googleUser("g2", "sasha@akarpov.ru"), meta); err != nil {
		t.Fatalf("allowed domain: %v", err)
	}
}

func TestLinkFlows(t *testing.T) {
	ctx := context.Background()
	s, repo, clk := newService(t, withGoogle)
	alice := register(t, s, "alice@example.com")
	x := googleUser("g3", "ALICE@example.com")
	iss, err := s.ExternalLogin(ctx, x, meta)
	if err != nil || iss.User.ID != alice.ID {
		t.Fatalf("auto link = %+v %v", iss, err)
	}
	if got, _ := repo.UserByID(ctx, alice.ID); !got.HasPassword() {
		t.Fatal("a verified account keeps its password when Google is linked")
	}
	if again, err := s.ExternalLogin(ctx, x, meta); err != nil || again.User.ID != alice.ID {
		t.Fatalf("login after link = %v", err)
	}
	bob := register(t, s, "bob@example.com")
	if err := s.LinkIdentity(ctx, bob.ID, x, meta); !errors.Is(err, ErrIdentityTaken) {
		t.Fatalf("taken err = %v", err)
	}
	if _, err := s.ExternalLogin(ctx, googleUser("g4", "alice@example.com"), meta); !errors.Is(err, ErrAlreadyLinked) {
		t.Fatalf("second google account err = %v", err)
	}

	p := login(t, s, "alice@example.com")
	clk.Advance(11 * time.Minute)
	if err := s.ExternalReauth(ctx, p, googleUser("g9", "alice@example.com"), meta); !errors.Is(err, ErrWrongIdentity) {
		t.Fatalf("wrong identity err = %v", err)
	}
	if err := s.ExternalReauth(ctx, p, x, meta); err != nil || !p.Fresh(clk.Now()) {
		t.Fatalf("reauth = %v", err)
	}

	sv, repo2, _ := newService(t, func(o *Options) {
		withGoogle(o)
		o.VerifyEmail = true
	})
	pre := register(t, sv, "victim@example.com")
	if _, err := sv.Login(ctx, "victim@example.com", testPassword, meta); !errors.Is(err, ErrUnverified) {
		t.Fatalf("an unverified account must not sign in with a password: %v", err)
	}
	squatter, err := sv.StartSession(ctx, pre, meta)
	if err != nil {
		t.Fatal(err)
	}
	vi, err := sv.ExternalLogin(ctx, googleUser("g7", "victim@example.com"), meta)
	if err != nil || vi.User.ID != pre.ID {
		t.Fatalf("unverified adopt = %+v %v", vi, err)
	}
	if got, _ := repo2.UserByID(ctx, pre.ID); got.HasPassword() {
		t.Fatal("the password of an unverified account must be removed on Google sign-in")
	}
	if _, err := sv.Authenticate(ctx, squatter.Token); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("earlier session err = %v", err)
	}

	solo, err := s.ExternalLogin(ctx, googleUser("g5", "solo@example.com"), meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UnlinkIdentity(ctx, solo.User.ID, domain.ProviderGoogle, meta); !errors.Is(err, ErrLastLogin) {
		t.Fatalf("last login err = %v", err)
	}
	if err := s.UnlinkIdentity(ctx, alice.ID, domain.ProviderGoogle, meta); err != nil {
		t.Fatal(err)
	}
}

func TestChangePassword(t *testing.T) {
	ctx := context.Background()
	s, repo, clk := newService(t, withGoogle)
	register(t, s, "alice@example.com")
	p := login(t, s, "alice@example.com")
	other, err := s.Login(ctx, "alice@example.com", testPassword, meta)
	if err != nil {
		t.Fatal(err)
	}
	err = s.ChangePassword(ctx, p, "wrong password", "brand new pass", meta)
	if got := fieldsOf(t, err); !slices.Equal(got, []string{"current_password"}) {
		t.Fatalf("fields = %v", got)
	}
	if err := s.ChangePassword(ctx, p, testPassword, "brand new pass", meta); err != nil {
		t.Fatal(err)
	}
	if _, ok := repo.sessions[p.SessionID]; !ok {
		t.Fatal("current session must survive")
	}
	if _, err := s.Authenticate(ctx, other.Token); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("other session err = %v", err)
	}
	if _, err := s.Login(ctx, "alice@example.com", "brand new pass", meta); err != nil {
		t.Fatal(err)
	}

	solo, err := s.ExternalLogin(ctx, googleUser("g1", "solo@example.com"), meta)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := s.Authenticate(ctx, solo.Token)
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(11 * time.Minute)
	if err := s.ChangePassword(ctx, sp, "", "solo password 1", meta); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("stale set err = %v", err)
	}
	if err := s.MarkReauth(ctx, sp, meta); err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePassword(ctx, sp, "", "solo password 1", meta); err != nil {
		t.Fatal(err)
	}
}
