package pg

import (
	"context"
	"encoding/json"
	"net/netip"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/db/sqlc"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

func (s *Store) CreateSession(ctx context.Context, x domain.Session) error {
	return mapErr(s.q.CreateSession(ctx, sqlc.CreateSessionParams{
		ID:                x.ID,
		TokenHash:         x.TokenHash,
		UserID:            x.UserID,
		CSRFSecret:        x.CSRFSecret,
		IP:                addrPtr(x.IP),
		UserAgent:         x.UserAgent,
		CreatedAt:         x.CreatedAt,
		LastSeenAt:        x.LastSeenAt,
		ExpiresAt:         x.ExpiresAt,
		AbsoluteExpiresAt: x.AbsoluteExpiresAt,
		ReauthAt:          x.ReauthAt,
	}))
}

func (s *Store) SessionByTokenHash(ctx context.Context, hash []byte) (domain.Session, error) {
	row, err := s.q.GetSessionByTokenHash(ctx, hash)
	if err != nil {
		return domain.Session{}, mapErr(err)
	}
	return toSession(row), nil
}

func (s *Store) TouchSession(ctx context.Context, id domain.ID, lastSeen, expires time.Time) error {
	return mapErr(s.q.TouchSession(ctx, sqlc.TouchSessionParams{ID: id, LastSeenAt: lastSeen, ExpiresAt: expires}))
}

func (s *Store) SetSessionReauth(ctx context.Context, id domain.ID, at time.Time) error {
	return mapErr(s.q.SetSessionReauth(ctx, sqlc.SetSessionReauthParams{ID: id, ReauthAt: &at}))
}

func (s *Store) DeleteSession(ctx context.Context, userID, id domain.ID) error {
	return affected(s.q.DeleteSession(ctx, sqlc.DeleteSessionParams{ID: id, UserID: userID}))
}

func (s *Store) DeleteUserSessions(ctx context.Context, userID, except domain.ID) (int64, error) {
	n, err := s.q.DeleteUserSessionsExcept(ctx, sqlc.DeleteUserSessionsExceptParams{UserID: userID, ID: except})
	return n, mapErr(err)
}

func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	n, err := s.q.DeleteExpiredSessions(ctx, now)
	return n, mapErr(err)
}

func (s *Store) ListSessions(ctx context.Context, userID domain.ID) ([]domain.Session, error) {
	rows, err := s.q.ListUserSessions(ctx, userID)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]domain.Session, len(rows))
	for i, r := range rows {
		out[i] = toSession(r)
	}
	return out, nil
}

func (s *Store) CreateAPIToken(ctx context.Context, t domain.APIToken) error {
	return mapErr(s.q.CreateAPIToken(ctx, sqlc.CreateAPITokenParams{
		ID:         t.ID,
		UserID:     t.UserID,
		Name:       t.Name,
		Prefix:     t.Prefix,
		SecretHash: t.SecretHash,
		Scopes:     t.Scopes,
		CreatedAt:  t.CreatedAt,
		ExpiresAt:  t.ExpiresAt,
	}))
}

func (s *Store) APITokenByPrefix(ctx context.Context, prefix string) (domain.APIToken, error) {
	row, err := s.q.GetAPITokenByPrefix(ctx, prefix)
	if err != nil {
		return domain.APIToken{}, mapErr(err)
	}
	return toAPIToken(row), nil
}

func (s *Store) TouchAPIToken(ctx context.Context, id domain.ID, at time.Time, ip netip.Addr) error {
	return mapErr(s.q.TouchAPIToken(ctx, sqlc.TouchAPITokenParams{ID: id, LastUsedAt: &at, LastUsedIP: addrPtr(ip)}))
}

func (s *Store) ListAPITokens(ctx context.Context, userID domain.ID) ([]domain.APIToken, error) {
	rows, err := s.q.ListUserAPITokens(ctx, userID)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]domain.APIToken, len(rows))
	for i, r := range rows {
		out[i] = toAPIToken(r)
	}
	return out, nil
}

func (s *Store) RevokeAPIToken(ctx context.Context, userID, id domain.ID, at time.Time) error {
	return affected(s.q.RevokeAPIToken(ctx, sqlc.RevokeAPITokenParams{ID: id, UserID: userID, RevokedAt: &at}))
}

func (s *Store) Audit(ctx context.Context, e domain.AuditEntry) error {
	meta := []byte("{}")
	if len(e.Meta) > 0 {
		b, err := json.Marshal(e.Meta)
		if err != nil {
			return err
		}
		meta = b
	}
	var user *domain.ID
	if e.UserID != domain.NilID {
		user = &e.UserID
	}
	return mapErr(s.q.InsertAudit(ctx, sqlc.InsertAuditParams{
		UserID:    user,
		Action:    e.Action,
		IP:        addrPtr(e.IP),
		UserAgent: e.UserAgent,
		Meta:      meta,
	}))
}

func toSession(r sqlc.Session) domain.Session {
	return domain.Session{
		ID:                r.ID,
		UserID:            r.UserID,
		TokenHash:         r.TokenHash,
		CSRFSecret:        r.CSRFSecret,
		IP:                addr(r.IP),
		UserAgent:         r.UserAgent,
		CreatedAt:         r.CreatedAt,
		LastSeenAt:        r.LastSeenAt,
		ExpiresAt:         r.ExpiresAt,
		AbsoluteExpiresAt: r.AbsoluteExpiresAt,
		ReauthAt:          r.ReauthAt,
	}
}

func toAPIToken(r sqlc.APIToken) domain.APIToken {
	return domain.APIToken{
		ID:         r.ID,
		UserID:     r.UserID,
		Name:       r.Name,
		Prefix:     r.Prefix,
		SecretHash: r.SecretHash,
		Scopes:     r.Scopes,
		CreatedAt:  r.CreatedAt,
		ExpiresAt:  r.ExpiresAt,
		LastUsedAt: r.LastUsedAt,
		LastUsedIP: addr(r.LastUsedIP),
		RevokedAt:  r.RevokedAt,
	}
}
