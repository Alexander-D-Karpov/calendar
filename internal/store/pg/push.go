package pg

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const pushColumns = `id, user_id, endpoint, p256dh, auth, user_agent, created_at, last_ok_at, failures`

const (
	insertPushSQL = `INSERT INTO push_subscriptions (id, user_id, endpoint, p256dh, auth, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (endpoint) DO UPDATE SET user_id = EXCLUDED.user_id, p256dh = EXCLUDED.p256dh,
			auth = EXCLUDED.auth, user_agent = EXCLUDED.user_agent, failures = 0
		RETURNING ` + pushColumns
	listPushSQL   = `SELECT ` + pushColumns + ` FROM push_subscriptions WHERE user_id = $1 ORDER BY created_at DESC`
	getPushSQL    = `SELECT ` + pushColumns + ` FROM push_subscriptions WHERE id = $1 AND user_id = $2`
	deletePushSQL = `DELETE FROM push_subscriptions WHERE id = $1 AND user_id = $2`
	dropPushSQL   = `DELETE FROM push_subscriptions WHERE id = $1`
	okPushSQL     = `UPDATE push_subscriptions SET last_ok_at = $2, failures = 0 WHERE id = $1`
	failPushSQL   = `UPDATE push_subscriptions SET failures = failures + 1 WHERE id = $1`
)

func scanPush(row pgx.Row) (domain.PushSubscription, error) {
	var p domain.PushSubscription
	var failures int32
	err := row.Scan(&p.ID, &p.UserID, &p.Endpoint, &p.P256dh, &p.Auth, &p.UserAgent, &p.CreatedAt, &p.LastOKAt, &failures)
	p.Failures = int(failures)
	return p, err
}

func (s *Store) AddPushSubscription(ctx context.Context, p domain.PushSubscription) (domain.PushSubscription, error) {
	out, err := scanPush(s.pool.QueryRow(ctx, insertPushSQL, p.ID, p.UserID, p.Endpoint, p.P256dh, p.Auth, p.UserAgent))
	return out, mapErr(err)
}

func (s *Store) PushSubscriptions(ctx context.Context, user domain.ID) ([]domain.PushSubscription, error) {
	rows, err := s.pool.Query(ctx, listPushSQL, user)
	if err != nil {
		return nil, mapErr(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.PushSubscription, error) { return scanPush(r) })
	return out, mapErr(err)
}

func (s *Store) PushSubscription(ctx context.Context, user, id domain.ID) (domain.PushSubscription, error) {
	p, err := scanPush(s.pool.QueryRow(ctx, getPushSQL, id, user))
	return p, mapErr(err)
}

func (s *Store) DeletePushSubscription(ctx context.Context, user, id domain.ID) error {
	tag, err := s.pool.Exec(ctx, deletePushSQL, id, user)
	return affected(tag.RowsAffected(), err)
}

func (s *Store) DropPushSubscription(ctx context.Context, id domain.ID) error {
	_, err := s.pool.Exec(ctx, dropPushSQL, id)
	return mapErr(err)
}

func (s *Store) TouchPushSubscription(ctx context.Context, id domain.ID, at time.Time, ok bool) error {
	sql := failPushSQL
	args := []any{id}
	if ok {
		sql, args = okPushSQL, []any{id, at}
	}
	_, err := s.pool.Exec(ctx, sql, args...)
	return mapErr(err)
}
