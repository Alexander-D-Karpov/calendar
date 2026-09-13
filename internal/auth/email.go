package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const (
	verifyTTL = 24 * time.Hour
	resetTTL  = time.Hour
)

var (
	ErrUnverified = fmt.Errorf("%w: confirm your email address first", domain.ErrForbidden)
	ErrBadToken   = fmt.Errorf("%w: this link expired or was already used", domain.ErrInvalid)
)

func (s *Service) IssueVerify(ctx context.Context, u domain.User) (string, bool, error) {
	if !s.verify || u.Verified() {
		return "", false, nil
	}
	tok, err := s.issue(ctx, u.ID, domain.PurposeVerify, verifyTTL)
	if err != nil {
		return "", false, err
	}
	return tok, true, nil
}

func (s *Service) ConfirmVerify(ctx context.Context, raw string, m Meta) (domain.User, error) {
	if err := s.loginIP.Check("verify:" + m.IP.String()); err != nil {
		return domain.User{}, err
	}
	now := s.clock.Now().UTC()
	t, err := s.repo.ConsumeEmailToken(ctx, domain.PurposeVerify, crypto.HashToken(raw), now)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.User{}, ErrBadToken
	}
	if err != nil {
		return domain.User{}, err
	}
	if err := s.repo.SetEmailVerified(ctx, t.UserID, now); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return domain.User{}, err
	}
	u, err := s.repo.UserByID(ctx, t.UserID)
	if err != nil {
		return domain.User{}, err
	}
	s.audit(ctx, u.ID, "user.verify", m, nil)
	return u, nil
}

// IssueReset says nothing about whether the address exists: an unknown address
// and one without a password both return no token and no error.
func (s *Service) IssueReset(ctx context.Context, email string, m Meta) (domain.User, string, bool, error) {
	if err := s.loginIP.Check("reset:" + m.IP.String()); err != nil {
		return domain.User{}, "", false, err
	}
	key, err := NormalizeEmail(email)
	if err != nil {
		return domain.User{}, "", false, nil
	}
	u, err := s.repo.UserByEmail(ctx, key)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return domain.User{}, "", false, nil
	case err != nil:
		return domain.User{}, "", false, err
	case !u.HasPassword() || u.Disabled():
		return domain.User{}, "", false, nil
	}
	tok, err := s.issue(ctx, u.ID, domain.PurposeReset, resetTTL)
	if err != nil {
		return domain.User{}, "", false, err
	}
	return u, tok, true, nil
}

func (s *Service) ResetPassword(ctx context.Context, raw, password string, m Meta) error {
	now := s.clock.Now().UTC()
	t, err := s.repo.ConsumeEmailToken(ctx, domain.PurposeReset, crypto.HashToken(raw), now)
	if errors.Is(err, domain.ErrNotFound) {
		return ErrBadToken
	}
	if err != nil {
		return err
	}
	if err := ValidatePassword(password, s.minLen); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(ctx, password)
	if err != nil {
		return err
	}
	if err := s.repo.SetPasswordHash(ctx, t.UserID, hash); err != nil {
		return err
	}
	if _, err := s.repo.DeleteUserSessions(ctx, t.UserID, domain.NilID); err != nil {
		return err
	}
	if err := s.repo.SetEmailVerified(ctx, t.UserID, now); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	s.audit(ctx, t.UserID, "user.password_reset", m, nil)
	return nil
}

// issue drops any earlier token for the purpose, so only the newest link works.
func (s *Service) issue(ctx context.Context, user domain.ID, purpose string, ttl time.Duration) (string, error) {
	if err := s.repo.DeleteEmailTokens(ctx, user, purpose); err != nil {
		return "", err
	}
	tok := crypto.RandomToken(32)
	now := s.clock.Now().UTC()
	err := s.repo.CreateEmailToken(ctx, domain.EmailToken{
		ID:        domain.NewID(),
		UserID:    user,
		Purpose:   purpose,
		TokenHash: crypto.HashToken(tok),
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	})
	if err != nil {
		return "", err
	}
	return tok, nil
}
