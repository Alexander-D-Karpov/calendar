package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

var (
	ErrIdentityTaken    = fmt.Errorf("%w: this Google account is linked to another user", domain.ErrConflict)
	ErrAlreadyLinked    = fmt.Errorf("%w: a Google account is already linked", domain.ErrConflict)
	ErrLastLogin        = fmt.Errorf("%w: set a password before unlinking the only sign-in method", domain.ErrConflict)
	ErrEmailUnverified  = fmt.Errorf("%w: the Google account email is not verified", domain.ErrForbidden)
	ErrDomainNotAllowed = fmt.Errorf("%w: this email domain cannot register", domain.ErrForbidden)
	ErrWrongIdentity    = fmt.Errorf("%w: sign in with the Google account linked to this user", domain.ErrForbidden)
)

type External struct {
	Provider      string
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Timezone      string
}

func (s *Service) ExternalLogin(ctx context.Context, x External, m Meta) (*Issued, error) {
	if err := s.loginIP.Check("login:" + m.IP.String()); err != nil {
		return nil, err
	}
	id, err := s.repo.IdentityBySubject(ctx, x.Provider, x.Subject)
	switch {
	case err == nil:
		u, err := s.repo.UserByID(ctx, id.UserID)
		if err != nil {
			return nil, err
		}
		return s.externalSession(ctx, u, x, m, "login")
	case !errors.Is(err, domain.ErrNotFound):
		return nil, err
	}
	if !x.EmailVerified {
		return nil, ErrEmailUnverified
	}
	email, err := NormalizeEmail(x.Email)
	if err != nil {
		return nil, ErrEmailUnverified
	}
	u, err := s.repo.UserByEmail(ctx, email)
	switch {
	case err == nil:
		if err := s.adopt(ctx, u, x, m); err != nil {
			return nil, err
		}
		return s.externalSession(ctx, u, x, m, "login")
	case !errors.Is(err, domain.ErrNotFound):
		return nil, err
	}
	u, err = s.externalSignup(ctx, x, email)
	if err != nil {
		return nil, err
	}
	return s.externalSession(ctx, u, x, m, "user.register")
}

// adopt links Google to an account that already owns the same address. An
// unverified account may have been registered by someone else with a password
// they know, so that password and its sessions go before the link.
func (s *Service) adopt(ctx context.Context, u domain.User, x External, m Meta) error {
	if u.Disabled() {
		return ErrDisabled
	}
	if !u.Verified() && u.HasPassword() {
		if err := s.repo.SetPasswordHash(ctx, u.ID, ""); err != nil {
			return err
		}
		if _, err := s.repo.DeleteUserSessions(ctx, u.ID, domain.NilID); err != nil {
			return err
		}
	}
	return s.LinkIdentity(ctx, u.ID, x, m)
}

func (s *Service) externalSignup(ctx context.Context, x External, email string) (domain.User, error) {
	if !s.reg.Enabled || !s.reg.GoogleEnabled {
		return domain.User{}, ErrRegistrationClosed
	}
	if !s.reg.EmailAllowed(email) {
		return domain.User{}, ErrDomainNotAllowed
	}
	now := s.clock.Now().UTC()
	tz := s.defaultTZ
	if domain.ValidTimezone(x.Timezone) {
		tz = x.Timezone
	}
	return s.repo.CreateUser(ctx, domain.NewUser{
		ID:              domain.NewID(),
		Email:           email,
		EmailVerifiedAt: &now,
		DisplayName:     clip(strings.TrimSpace(x.Name), maxDisplayName),
		Timezone:        tz,
		Identity:        &domain.Identity{ID: domain.NewID(), Provider: x.Provider, Subject: x.Subject, Email: email, LinkedAt: now},
	})
}

func (s *Service) externalSession(ctx context.Context, u domain.User, x External, m Meta, action string) (*Issued, error) {
	if u.Disabled() {
		return nil, ErrDisabled
	}
	iss, err := s.StartSession(ctx, u, m)
	if err != nil {
		return nil, err
	}
	s.audit(ctx, u.ID, action, m, map[string]any{"method": x.Provider})
	return iss, nil
}

func (s *Service) LinkIdentity(ctx context.Context, userID domain.ID, x External, m Meta) error {
	id, err := s.repo.IdentityBySubject(ctx, x.Provider, x.Subject)
	switch {
	case err == nil && id.UserID == userID:
		return nil
	case err == nil:
		return ErrIdentityTaken
	case !errors.Is(err, domain.ErrNotFound):
		return err
	}
	email, err := NormalizeEmail(x.Email)
	if err != nil {
		email = strings.ToLower(strings.TrimSpace(x.Email))
	}
	err = s.repo.CreateIdentity(ctx, domain.Identity{
		ID: domain.NewID(), UserID: userID, Provider: x.Provider, Subject: x.Subject, Email: email, LinkedAt: s.clock.Now().UTC(),
	})
	if errors.Is(err, domain.ErrConflict) {
		return ErrAlreadyLinked
	}
	if err != nil {
		return err
	}
	s.audit(ctx, userID, "identity.link", m, map[string]any{"provider": x.Provider, "email": email})
	return nil
}

func (s *Service) UnlinkIdentity(ctx context.Context, userID domain.ID, provider string, m Meta) error {
	u, err := s.repo.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if !u.HasPassword() {
		return ErrLastLogin
	}
	if err := s.repo.DeleteIdentity(ctx, userID, provider); err != nil {
		return err
	}
	s.audit(ctx, userID, "identity.unlink", m, map[string]any{"provider": provider})
	return nil
}

func (s *Service) ExternalReauth(ctx context.Context, p *Principal, x External, m Meta) error {
	if !p.IsSession() {
		return ErrReauthRequired
	}
	id, err := s.repo.IdentityBySubject(ctx, x.Provider, x.Subject)
	if errors.Is(err, domain.ErrNotFound) || (err == nil && id.UserID != p.UserID) {
		s.audit(ctx, p.UserID, "reauth.failed", m, map[string]any{"method": x.Provider})
		return ErrWrongIdentity
	}
	if err != nil {
		return err
	}
	return s.MarkReauth(ctx, p, m)
}

func (s *Service) Identities(ctx context.Context, userID domain.ID) ([]domain.Identity, error) {
	return s.repo.ListIdentities(ctx, userID)
}

func (s *Service) ChangePassword(ctx context.Context, p *Principal, current, next string, m Meta) error {
	if !p.IsSession() {
		return ErrReauthRequired
	}
	u, err := s.repo.UserByID(ctx, p.UserID)
	if err != nil {
		return err
	}
	if u.HasPassword() {
		if err := s.loginEmail.Check("reauth:" + p.UserID.String()); err != nil {
			return err
		}
		ok := false
		if len(current) <= MaxPasswordBytes {
			if ok, _, err = s.hasher.Verify(ctx, current, u.PasswordHash); err != nil {
				return err
			}
		}
		if !ok {
			s.audit(ctx, u.ID, "password.failed", m, nil)
			var v domain.ValidationError
			v.Add("current_password", "is incorrect")
			return v.Err()
		}
	} else if !p.Fresh(s.clock.Now()) {
		return ErrReauthRequired
	}
	if err := ValidatePassword(next, s.minLen); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(ctx, next)
	if err != nil {
		return err
	}
	if err := s.repo.SetPasswordHash(ctx, u.ID, hash); err != nil {
		return err
	}
	if _, err := s.repo.DeleteUserSessions(ctx, u.ID, p.SessionID); err != nil {
		return err
	}
	action := "user.password_change"
	if !u.HasPassword() {
		action = "user.password_set"
	}
	s.audit(ctx, u.ID, action, m, nil)
	return nil
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
