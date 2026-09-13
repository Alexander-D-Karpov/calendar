package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

func withVerify(o *Options) {
	o.VerifyEmail = true
}

func TestVerifyRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newService(t, withVerify)
	u := register(t, s, "sasha@example.com")
	if u.Verified() {
		t.Fatal("registration must not pre-verify when SMTP is on")
	}
	if _, err := s.Login(ctx, "sasha@example.com", testPassword, meta); !errors.Is(err, ErrUnverified) {
		t.Fatalf("login before confirm = %v", err)
	}
	tok, sent, err := s.IssueVerify(ctx, u)
	if err != nil || !sent || tok == "" {
		t.Fatalf("issue = %q %v %v", tok, sent, err)
	}
	got, err := s.ConfirmVerify(ctx, tok, meta)
	if err != nil || !got.Verified() {
		t.Fatalf("confirm = %+v %v", got, err)
	}
	if _, err := s.ConfirmVerify(ctx, tok, meta); !errors.Is(err, ErrBadToken) {
		t.Fatalf("replay = %v", err)
	}
	if _, err := s.Login(ctx, "sasha@example.com", testPassword, meta); err != nil {
		t.Fatalf("login after confirm = %v", err)
	}
	if _, sent, _ := s.IssueVerify(ctx, got); sent {
		t.Fatal("a verified account needs no link")
	}
}

func TestVerifyTokenExpires(t *testing.T) {
	ctx := context.Background()
	s, _, clk := newService(t, withVerify)
	u := register(t, s, "sasha@example.com")
	tok, _, err := s.IssueVerify(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(verifyTTL + time.Minute)
	if _, err := s.ConfirmVerify(ctx, tok, meta); !errors.Is(err, ErrBadToken) {
		t.Fatalf("expired = %v", err)
	}
}

func TestVerifyReissueInvalidatesTheFirst(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newService(t, withVerify)
	u := register(t, s, "sasha@example.com")
	first, _, err := s.IssueVerify(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := s.IssueVerify(ctx, u)
	if err != nil || second == first {
		t.Fatalf("second = %q %v", second, err)
	}
	if _, err := s.ConfirmVerify(ctx, first, meta); !errors.Is(err, ErrBadToken) {
		t.Fatalf("superseded token = %v", err)
	}
	if _, err := s.ConfirmVerify(ctx, second, meta); err != nil {
		t.Fatalf("newest token = %v", err)
	}
}

func TestResetHidesAccountExistence(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newService(t, nil)
	register(t, s, "sasha@example.com")
	u, tok, sent, err := s.IssueReset(ctx, "nobody@example.com", meta)
	if err != nil || sent || tok != "" || u.ID != domain.NilID {
		t.Fatalf("unknown address = %+v %q %v %v", u, tok, sent, err)
	}
	if _, _, sent, err := s.IssueReset(ctx, "not an address", meta); err != nil || sent {
		t.Fatalf("malformed address = %v %v", sent, err)
	}
}

func TestResetPassword(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newService(t, nil)
	register(t, s, "sasha@example.com")
	old, err := s.Login(ctx, "sasha@example.com", testPassword, meta)
	if err != nil {
		t.Fatal(err)
	}
	_, tok, sent, err := s.IssueReset(ctx, "sasha@example.com", meta)
	if err != nil || !sent {
		t.Fatalf("issue = %v %v", sent, err)
	}
	if err := s.ResetPassword(ctx, tok, "brand new pass", meta); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, old.Token); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("every earlier session must die: %v", err)
	}
	if _, err := s.Login(ctx, "sasha@example.com", "brand new pass", meta); err != nil {
		t.Fatalf("login with the new password = %v", err)
	}
	if err := s.ResetPassword(ctx, tok, "another pass here", meta); !errors.Is(err, ErrBadToken) {
		t.Fatalf("replay = %v", err)
	}
}

func TestResetVerifiesTheAddress(t *testing.T) {
	ctx := context.Background()
	s, repo, _ := newService(t, withVerify)
	u := register(t, s, "sasha@example.com")
	_, tok, sent, err := s.IssueReset(ctx, "sasha@example.com", meta)
	if err != nil || !sent {
		t.Fatalf("issue = %v %v", sent, err)
	}
	if err := s.ResetPassword(ctx, tok, "brand new pass", meta); err != nil {
		t.Fatal(err)
	}
	got, err := repo.UserByID(ctx, u.ID)
	if err != nil || !got.Verified() {
		t.Fatalf("holding the link proves the address: %+v %v", got, err)
	}
	if _, err := s.Login(ctx, "sasha@example.com", "brand new pass", meta); err != nil {
		t.Fatalf("login after reset = %v", err)
	}
}
