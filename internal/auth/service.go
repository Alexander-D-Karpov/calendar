package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"net/netip"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/config"
	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/ratelimit"
)

const (
	maxUserAgent   = 512
	maxDisplayName = 100
	maxTokenName   = 100
	maxTokenTTL    = 5 * 365 * 24 * time.Hour
)

var (
	ErrInvalidCredentials = fmt.Errorf("%w: invalid email or password", domain.ErrUnauthorized)
	ErrSessionExpired     = fmt.Errorf("%w: session expired", domain.ErrUnauthorized)
	ErrInvalidToken       = fmt.Errorf("%w: invalid api token", domain.ErrUnauthorized)
	ErrRegistrationClosed = fmt.Errorf("%w: registration is closed", domain.ErrForbidden)
	ErrDisabled           = fmt.Errorf("%w: account is disabled", domain.ErrForbidden)
	ErrReauthRequired     = fmt.Errorf("%w: recent authentication required", domain.ErrForbidden)
	ErrNoPassword         = fmt.Errorf("%w: account has no password", domain.ErrInvalid)
)

type Meta struct {
	IP        netip.Addr
	UserAgent string
}

type Options struct {
	Repo              Repository
	Hasher            *Hasher
	Clock             clock.Clock
	Logger            *slog.Logger
	Policy            SessionPolicy
	Registration      config.Registration
	PasswordMinLength int
	LoginRate         config.Rate
	DefaultTimezone   string
	VerifyEmail       bool
}

type Service struct {
	repo       Repository
	hasher     *Hasher
	clock      clock.Clock
	logger     *slog.Logger
	policy     SessionPolicy
	reg        config.Registration
	minLen     int
	defaultTZ  string
	verify     bool
	loginIP    *ratelimit.Limiter
	loginEmail *ratelimit.Limiter
}

type RegisterInput struct {
	Email       string
	Password    string
	DisplayName string
	Timezone    string
}

type CreateUserInput struct {
	RegisterInput
	Verified bool
}

type Issued struct {
	Token   string
	Session domain.Session
	User    domain.User
}

type TokenInput struct {
	Name      string
	Scopes    []string
	ExpiresAt *time.Time
}

func NewService(o Options) *Service {
	if o.Clock == nil {
		o.Clock = clock.New()
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.DefaultTimezone == "" {
		o.DefaultTimezone = "UTC"
	}
	if o.PasswordMinLength <= 0 {
		o.PasswordMinLength = 10
	}
	if o.LoginRate.Count <= 0 || o.LoginRate.Per <= 0 {
		o.LoginRate = config.Rate{Count: 10, Per: 15 * time.Minute}
	}
	return &Service{
		repo:       o.Repo,
		hasher:     o.Hasher,
		clock:      o.Clock,
		logger:     o.Logger,
		policy:     o.Policy,
		reg:        o.Registration,
		minLen:     o.PasswordMinLength,
		defaultTZ:  o.DefaultTimezone,
		verify:     o.VerifyEmail,
		loginIP:    ratelimit.New(o.LoginRate, o.Clock),
		loginEmail: ratelimit.New(o.LoginRate, o.Clock),
	}
}

func (s *Service) Policy() SessionPolicy {
	return s.policy
}

func (s *Service) Register(ctx context.Context, in RegisterInput, m Meta) (domain.User, error) {
	if !s.reg.Enabled || !s.reg.PasswordEnabled {
		return domain.User{}, ErrRegistrationClosed
	}
	if err := s.loginIP.Check("register:" + m.IP.String()); err != nil {
		return domain.User{}, err
	}
	u, err := s.createUser(ctx, in, false, true)
	if err != nil {
		return domain.User{}, err
	}
	s.audit(ctx, u.ID, "user.register", m, nil)
	return u, nil
}

func (s *Service) CreateUser(ctx context.Context, in CreateUserInput, m Meta) (domain.User, error) {
	u, err := s.createUser(ctx, in.RegisterInput, in.Verified, false)
	if err != nil {
		return domain.User{}, err
	}
	s.audit(ctx, u.ID, "user.create", m, map[string]any{"via": "admin"})
	return u, nil
}

func (s *Service) createUser(ctx context.Context, in RegisterInput, verified, public bool) (domain.User, error) {
	var v domain.ValidationError
	email, err := NormalizeEmail(in.Email)
	switch {
	case err != nil:
		v.Add("email", "must be a valid email address")
	case public && !s.reg.EmailAllowed(email):
		v.Add("email", "this email domain cannot register")
	}
	if err := ValidatePassword(in.Password, s.minLen); err != nil {
		var pe *domain.ValidationError
		if errors.As(err, &pe) {
			v.Fields = append(v.Fields, pe.Fields...)
		}
	}
	name := strings.TrimSpace(in.DisplayName)
	if utf8.RuneCountInString(name) > maxDisplayName {
		v.Addf("display_name", "must be at most %d characters", maxDisplayName)
	}
	tz := strings.TrimSpace(in.Timezone)
	if tz == "" {
		tz = s.defaultTZ
	} else if _, err := time.LoadLocation(tz); err != nil || tz == "Local" {
		v.Add("timezone", "unknown timezone")
	}
	if err := v.Err(); err != nil {
		return domain.User{}, err
	}

	hash, err := s.hasher.Hash(ctx, in.Password)
	if err != nil {
		return domain.User{}, err
	}
	nu := domain.NewUser{
		ID:           domain.NewID(),
		Email:        email,
		PasswordHash: hash,
		DisplayName:  name,
		Timezone:     tz,
	}
	if verified || !s.verify {
		now := s.clock.Now().UTC()
		nu.EmailVerifiedAt = &now
	}
	u, err := s.repo.CreateUser(ctx, nu)
	if errors.Is(err, domain.ErrConflict) {
		var dup domain.ValidationError
		dup.Add("email", "is already registered")
		return domain.User{}, dup.Err()
	}
	return u, err
}

func (s *Service) Login(ctx context.Context, email, password string, m Meta) (*Issued, error) {
	key := strings.ToLower(strings.TrimSpace(email))
	if err := s.loginIP.Check("login:" + m.IP.String()); err != nil {
		return nil, err
	}
	if err := s.loginEmail.Check(key); err != nil {
		return nil, err
	}
	if key == "" || len(password) > MaxPasswordBytes {
		return nil, ErrInvalidCredentials
	}
	user, err := s.repo.UserByEmail(ctx, key)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		s.hasher.Dummy(ctx, password)
		return nil, ErrInvalidCredentials
	case err != nil:
		return nil, err
	case !user.HasPassword():
		s.hasher.Dummy(ctx, password)
		return nil, ErrInvalidCredentials
	}
	ok, rehash, err := s.hasher.Verify(ctx, password, user.PasswordHash)
	if err != nil {
		return nil, err
	}
	if !ok {
		s.audit(ctx, user.ID, "login.failed", m, nil)
		return nil, ErrInvalidCredentials
	}
	if user.Disabled() {
		return nil, ErrDisabled
	}
	if s.verify && !user.Verified() {
		return nil, ErrUnverified
	}
	s.loginEmail.Reset(key)
	if rehash {
		s.upgradeHash(ctx, user.ID, password)
	}
	iss, err := s.StartSession(ctx, user, m)
	if err != nil {
		return nil, err
	}
	s.audit(ctx, user.ID, "login", m, map[string]any{"method": "password"})
	return iss, nil
}

func (s *Service) StartSession(ctx context.Context, user domain.User, m Meta) (*Issued, error) {
	tok := NewSessionToken()
	now := s.clock.Now().UTC()
	exp, abs := s.policy.NewExpiry(now)
	sess := domain.Session{
		ID:                domain.NewID(),
		UserID:            user.ID,
		TokenHash:         tok.Hash,
		CSRFSecret:        crypto.RandomBytes(32),
		IP:                m.IP,
		UserAgent:         truncate(m.UserAgent, maxUserAgent),
		CreatedAt:         now,
		LastSeenAt:        now,
		ExpiresAt:         exp,
		AbsoluteExpiresAt: abs,
		ReauthAt:          &now,
	}
	if err := s.repo.CreateSession(ctx, sess); err != nil {
		return nil, err
	}
	return &Issued{Token: tok.Value, Session: sess, User: user}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (*Principal, error) {
	sess, err := s.repo.SessionByTokenHash(ctx, crypto.HashToken(token))
	if errors.Is(err, domain.ErrNotFound) {
		return nil, ErrSessionExpired
	}
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	if !now.Before(sess.ExpiresAt) || !now.Before(sess.AbsoluteExpiresAt) {
		if err := s.repo.DeleteSession(ctx, sess.UserID, sess.ID); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return nil, err
		}
		return nil, ErrSessionExpired
	}
	p := &Principal{
		UserID:     sess.UserID,
		SessionID:  sess.ID,
		CSRFSecret: sess.CSRFSecret,
		ExpiresAt:  sess.ExpiresAt,
	}
	if sess.ReauthAt != nil {
		p.ReauthAt = *sess.ReauthAt
	}
	if s.policy.NeedsTouch(sess.LastSeenAt, now) {
		exp := s.policy.Extend(now, sess.AbsoluteExpiresAt)
		if err := s.repo.TouchSession(ctx, sess.ID, now, exp); err != nil {
			return nil, err
		}
		p.ExpiresAt, p.Refreshed = exp, true
	}
	return p, nil
}

func (s *Service) Logout(ctx context.Context, p *Principal, m Meta) error {
	if !p.IsSession() {
		return nil
	}
	if err := s.repo.DeleteSession(ctx, p.UserID, p.SessionID); err != nil && !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	s.audit(ctx, p.UserID, "logout", m, nil)
	return nil
}

func (s *Service) Reauth(ctx context.Context, p *Principal, password string, m Meta) error {
	if !p.IsSession() {
		return ErrReauthRequired
	}
	if err := s.loginEmail.Check("reauth:" + p.UserID.String()); err != nil {
		return err
	}
	user, err := s.repo.UserByID(ctx, p.UserID)
	if err != nil {
		return err
	}
	if !user.HasPassword() {
		return ErrNoPassword
	}
	if len(password) > MaxPasswordBytes {
		return ErrInvalidCredentials
	}
	ok, _, err := s.hasher.Verify(ctx, password, user.PasswordHash)
	if err != nil {
		return err
	}
	if !ok {
		s.audit(ctx, p.UserID, "reauth.failed", m, nil)
		return ErrInvalidCredentials
	}
	return s.MarkReauth(ctx, p, m)
}

func (s *Service) MarkReauth(ctx context.Context, p *Principal, m Meta) error {
	now := s.clock.Now().UTC()
	if err := s.repo.SetSessionReauth(ctx, p.SessionID, now); err != nil {
		return err
	}
	p.ReauthAt = now
	s.audit(ctx, p.UserID, "reauth", m, nil)
	return nil
}

func (s *Service) ListSessions(ctx context.Context, userID domain.ID) ([]domain.Session, error) {
	return s.repo.ListSessions(ctx, userID)
}

func (s *Service) RevokeSession(ctx context.Context, userID, id domain.ID, m Meta) error {
	if err := s.repo.DeleteSession(ctx, userID, id); err != nil {
		return err
	}
	s.audit(ctx, userID, "session.revoke", m, map[string]any{"session_id": id.String()})
	return nil
}

func (s *Service) RevokeOtherSessions(ctx context.Context, p *Principal, m Meta) (int64, error) {
	n, err := s.repo.DeleteUserSessions(ctx, p.UserID, p.SessionID)
	if err != nil {
		return 0, err
	}
	s.audit(ctx, p.UserID, "session.revoke_others", m, map[string]any{"count": n})
	return n, nil
}

func (s *Service) CreateAPIToken(ctx context.Context, p *Principal, in TokenInput, m Meta) (string, domain.APIToken, error) {
	now := s.clock.Now().UTC()
	if !p.Fresh(now) {
		return "", domain.APIToken{}, ErrReauthRequired
	}
	var v domain.ValidationError
	name := strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(name); n == 0 || n > maxTokenName {
		v.Addf("name", "must be 1 to %d characters", maxTokenName)
	}
	scopes, err := ParseScopes(in.Scopes)
	if err != nil {
		v.Add("scopes", strings.TrimPrefix(err.Error(), domain.ErrInvalid.Error()+": "))
	}
	if in.ExpiresAt != nil && (!in.ExpiresAt.After(now) || in.ExpiresAt.Sub(now) > maxTokenTTL) {
		v.Add("expires_at", "must be in the future and within 5 years")
	}
	if err := v.Err(); err != nil {
		return "", domain.APIToken{}, err
	}

	tok := NewAPIToken()
	t := domain.APIToken{
		ID:         domain.NewID(),
		UserID:     p.UserID,
		Name:       name,
		Prefix:     tok.Prefix,
		SecretHash: tok.Hash,
		Scopes:     ScopeStrings(scopes),
		CreatedAt:  now,
	}
	if in.ExpiresAt != nil {
		e := in.ExpiresAt.UTC()
		t.ExpiresAt = &e
	}
	if err := s.repo.CreateAPIToken(ctx, t); err != nil {
		return "", domain.APIToken{}, err
	}
	s.audit(ctx, p.UserID, "api_token.create", m, map[string]any{"token_id": t.ID.String(), "name": name, "scopes": t.Scopes})
	return tok.Token, t, nil
}

func (s *Service) AuthenticateAPIToken(ctx context.Context, raw string, m Meta) (*Principal, error) {
	prefix, ok := ParseAPIToken(raw)
	if !ok {
		return nil, ErrInvalidToken
	}
	t, err := s.repo.APITokenByPrefix(ctx, prefix)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	if !VerifyAPIToken(raw, t.SecretHash) || !t.Active(now) {
		return nil, ErrInvalidToken
	}
	if t.LastUsedAt == nil || now.Sub(*t.LastUsedAt) >= TouchInterval || t.LastUsedIP != m.IP {
		if err := s.repo.TouchAPIToken(ctx, t.ID, now.UTC(), m.IP); err != nil {
			return nil, err
		}
	}
	return &Principal{UserID: t.UserID, TokenID: t.ID, Scopes: storedScopes(t.Scopes)}, nil
}

func (s *Service) ListAPITokens(ctx context.Context, userID domain.ID) ([]domain.APIToken, error) {
	return s.repo.ListAPITokens(ctx, userID)
}

func (s *Service) RevokeAPIToken(ctx context.Context, userID, id domain.ID, m Meta) error {
	if err := s.repo.RevokeAPIToken(ctx, userID, id, s.clock.Now().UTC()); err != nil {
		return err
	}
	s.audit(ctx, userID, "api_token.revoke", m, map[string]any{"token_id": id.String()})
	return nil
}

func (s *Service) ListUsers(ctx context.Context) ([]domain.User, error) {
	return s.repo.ListUsers(ctx)
}

func (s *Service) UserByEmail(ctx context.Context, email string) (domain.User, error) {
	return s.repo.UserByEmail(ctx, strings.TrimSpace(email))
}

func (s *Service) SetPassword(ctx context.Context, userID domain.ID, password string, m Meta) error {
	if err := ValidatePassword(password, s.minLen); err != nil {
		return err
	}
	hash, err := s.hasher.Hash(ctx, password)
	if err != nil {
		return err
	}
	if err := s.repo.SetPasswordHash(ctx, userID, hash); err != nil {
		return err
	}
	if _, err := s.repo.DeleteUserSessions(ctx, userID, domain.NilID); err != nil {
		return err
	}
	s.audit(ctx, userID, "user.password_set", m, nil)
	return nil
}

func (s *Service) SetDisabled(ctx context.Context, userID domain.ID, disabled bool, m Meta) error {
	var at *time.Time
	if disabled {
		now := s.clock.Now().UTC()
		at = &now
	}
	if err := s.repo.SetUserDisabled(ctx, userID, at); err != nil {
		return err
	}
	action := "user.enable"
	if disabled {
		action = "user.disable"
		if _, err := s.repo.DeleteUserSessions(ctx, userID, domain.NilID); err != nil {
			return err
		}
	}
	s.audit(ctx, userID, action, m, nil)
	return nil
}

func (s *Service) upgradeHash(ctx context.Context, id domain.ID, password string) {
	hash, err := s.hasher.Hash(ctx, password)
	if err == nil {
		err = s.repo.SetPasswordHash(ctx, id, hash)
	}
	if err != nil {
		s.logger.LogAttrs(ctx, slog.LevelWarn, "password rehash failed", slog.Any("err", err))
	}
}

func (s *Service) audit(ctx context.Context, userID domain.ID, action string, m Meta, meta map[string]any) {
	err := s.repo.Audit(context.WithoutCancel(ctx), domain.AuditEntry{
		UserID:    userID,
		Action:    action,
		IP:        m.IP,
		UserAgent: truncate(m.UserAgent, maxUserAgent),
		Meta:      meta,
	})
	if err != nil {
		s.logger.LogAttrs(ctx, slog.LevelWarn, "audit write failed", slog.String("action", action), slog.Any("err", err))
	}
}

func NormalizeEmail(s string) (string, error) {
	bad := fmt.Errorf("%w: invalid email address", domain.ErrInvalid)
	s = strings.TrimSpace(s)
	if len(s) < 3 || len(s) > 254 || strings.Count(s, "@") != 1 {
		return "", bad
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Name != "" || a.Address != s {
		return "", bad
	}
	local, host, _ := strings.Cut(s, "@")
	if local == "" || !strings.Contains(host, ".") || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return "", bad
	}
	return local + "@" + strings.ToLower(host), nil
}

func storedScopes(in []string) []Scope {
	out := make([]Scope, 0, len(in))
	for _, s := range in {
		if sc := Scope(s); slices.Contains(AllScopes, sc) {
			out = append(out, sc)
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

func (s *Service) User(ctx context.Context, id domain.ID) (domain.User, error) {
	return s.repo.UserByID(ctx, id)
}
