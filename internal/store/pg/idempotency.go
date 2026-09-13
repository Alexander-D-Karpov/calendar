package pg

import (
	"context"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const (
	claimIdempotencySQL = `INSERT INTO idempotency_keys (user_id, key, method, path, request_hash)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT (user_id, key) DO NOTHING`

	readIdempotencySQL = `SELECT request_hash, status, response_type, response_body
		FROM idempotency_keys WHERE user_id = $1 AND key = $2`

	saveIdempotencySQL = `UPDATE idempotency_keys SET status = $3, response_type = $4, response_body = $5
		WHERE user_id = $1 AND key = $2`

	dropIdempotencySQL = `DELETE FROM idempotency_keys WHERE user_id = $1 AND key = $2`
)

func (s *Store) ClaimIdempotency(ctx context.Context, user domain.ID, key, method, path string, hash []byte) (bool, error) {
	tag, err := s.pool.Exec(ctx, claimIdempotencySQL, user, key, method, path, hash)
	if err != nil {
		return false, mapErr(err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) Idempotency(ctx context.Context, user domain.ID, key string) (domain.IdempotentRecord, error) {
	var (
		rec    domain.IdempotentRecord
		status *int16
		ctype  *string
		body   []byte
	)
	err := s.pool.QueryRow(ctx, readIdempotencySQL, user, key).Scan(&rec.RequestHash, &status, &ctype, &body)
	if err != nil {
		return rec, mapErr(err)
	}
	if status != nil {
		rec.Status = int(*status)
	}
	rec.ContentType, rec.Body = text(ctype), body
	return rec, nil
}

func (s *Store) SaveIdempotency(ctx context.Context, user domain.ID, key string, status int, contentType string, body []byte) error {
	_, err := s.pool.Exec(ctx, saveIdempotencySQL, user, key, int16(status), textPtr(contentType), body)
	return mapErr(err)
}

// DropIdempotency frees the key after a failed attempt so the caller can retry
// with the same one.
func (s *Store) DropIdempotency(ctx context.Context, user domain.ID, key string) error {
	_, err := s.pool.Exec(ctx, dropIdempotencySQL, user, key)
	return mapErr(err)
}
