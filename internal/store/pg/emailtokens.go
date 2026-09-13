package pg

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const emailTokenColumns = `id, user_id, purpose, token_hash, coalesce(new_email, ''), created_at, expires_at, used_at`

const (
	insertEmailTokenSQL = `INSERT INTO email_tokens (id, user_id, purpose, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5)`

	// One statement, so a link cannot be replayed: the row is claimed and
	// returned together.
	consumeEmailTokenSQL = `UPDATE email_tokens SET used_at = $3
		WHERE token_hash = $1 AND purpose = $2 AND used_at IS NULL AND expires_at > $3
		RETURNING ` + emailTokenColumns

	deleteEmailTokensSQL = `DELETE FROM email_tokens WHERE user_id = $1 AND purpose = $2`

	setEmailVerifiedSQL = `UPDATE users SET email_verified_at = $2, updated_at = now()
		WHERE id = $1 AND email_verified_at IS NULL`
)

func scanEmailToken(row pgx.Row) (domain.EmailToken, error) {
	var t domain.EmailToken
	err := row.Scan(&t.ID, &t.UserID, &t.Purpose, &t.TokenHash, &t.NewEmail, &t.CreatedAt, &t.ExpiresAt, &t.UsedAt)
	return t, err
}

func (s *Store) CreateEmailToken(ctx context.Context, t domain.EmailToken) error {
	_, err := s.pool.Exec(ctx, insertEmailTokenSQL, t.ID, t.UserID, t.Purpose, t.TokenHash, t.ExpiresAt)
	return mapErr(err)
}

func (s *Store) ConsumeEmailToken(ctx context.Context, purpose string, hash []byte, at time.Time) (domain.EmailToken, error) {
	t, err := scanEmailToken(s.pool.QueryRow(ctx, consumeEmailTokenSQL, hash, purpose, at))
	return t, mapErr(err)
}

func (s *Store) DeleteEmailTokens(ctx context.Context, userID domain.ID, purpose string) error {
	_, err := s.pool.Exec(ctx, deleteEmailTokensSQL, userID, purpose)
	return mapErr(err)
}

func (s *Store) SetEmailVerified(ctx context.Context, id domain.ID, at time.Time) error {
	tag, err := s.pool.Exec(ctx, setEmailVerifiedSQL, id, at)
	return affected(tag.RowsAffected(), err)
}
