package auth

import (
	"context"
	"net/netip"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

type Repository interface {
	CreateUser(ctx context.Context, u domain.NewUser) (domain.User, error)
	UserByID(ctx context.Context, id domain.ID) (domain.User, error)
	UserByEmail(ctx context.Context, email string) (domain.User, error)
	ListUsers(ctx context.Context) ([]domain.User, error)
	SetPasswordHash(ctx context.Context, id domain.ID, hash string) error
	SetUserDisabled(ctx context.Context, id domain.ID, at *time.Time) error

	IdentityBySubject(ctx context.Context, provider, subject string) (domain.Identity, error)
	ListIdentities(ctx context.Context, userID domain.ID) ([]domain.Identity, error)
	CreateIdentity(ctx context.Context, id domain.Identity) error
	DeleteIdentity(ctx context.Context, userID domain.ID, provider string) error

	CreateSession(ctx context.Context, s domain.Session) error
	SessionByTokenHash(ctx context.Context, hash []byte) (domain.Session, error)
	TouchSession(ctx context.Context, id domain.ID, lastSeen, expires time.Time) error
	SetSessionReauth(ctx context.Context, id domain.ID, at time.Time) error
	DeleteSession(ctx context.Context, userID, id domain.ID) error
	DeleteUserSessions(ctx context.Context, userID, except domain.ID) (int64, error)
	ListSessions(ctx context.Context, userID domain.ID) ([]domain.Session, error)

	CreateAPIToken(ctx context.Context, t domain.APIToken) error
	APITokenByPrefix(ctx context.Context, prefix string) (domain.APIToken, error)
	TouchAPIToken(ctx context.Context, id domain.ID, at time.Time, ip netip.Addr) error
	ListAPITokens(ctx context.Context, userID domain.ID) ([]domain.APIToken, error)
	RevokeAPIToken(ctx context.Context, userID, id domain.ID, at time.Time) error

	CreateEmailToken(ctx context.Context, t domain.EmailToken) error
	ConsumeEmailToken(ctx context.Context, purpose string, hash []byte, at time.Time) (domain.EmailToken, error)
	DeleteEmailTokens(ctx context.Context, userID domain.ID, purpose string) error
	SetEmailVerified(ctx context.Context, id domain.ID, at time.Time) error

	Audit(ctx context.Context, e domain.AuditEntry) error
}
