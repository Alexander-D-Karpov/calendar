package pg

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Alexander-D-Karpov/calendar/internal/db/sqlc"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const subColumns = `id, owner_id, calendar_id, url_enc, url_host, (extract(epoch FROM refresh_interval))::bigint,
	next_refresh_at, status, coalesce(etag, ''), coalesce(last_modified, ''), content_hash, failures, last_attempt_at,
	last_ok_at, coalesce(last_error, '')`

const (
	insertSubSQL = `INSERT INTO ics_subscriptions (id, owner_id, calendar_id, url_enc, url_host, refresh_interval,
		next_refresh_at, status, etag, last_modified, last_attempt_at)
		VALUES ($1, $2, $3, $4, $5, make_interval(secs => $6::double precision), $7, $8, NULLIF($9, ''), NULLIF($10, ''), $11)`
	subSQL     = `SELECT ` + subColumns + ` FROM ics_subscriptions WHERE id = $1 AND owner_id = $2`
	subByIDSQL = `SELECT ` + subColumns + ` FROM ics_subscriptions WHERE id = $1`
	subsSQL    = `SELECT ` + subColumns + ` FROM ics_subscriptions WHERE owner_id = $1 ORDER BY created_at`
	saveSubSQL = `UPDATE ics_subscriptions SET next_refresh_at = $2, status = $3, etag = NULLIF($4, ''),
		last_modified = NULLIF($5, ''), content_hash = $6, failures = $7, last_attempt_at = $8, last_ok_at = $9,
		last_error = NULLIF($10, ''), updated_at = now() WHERE id = $1`
	dueSubsSQL = `SELECT id FROM ics_subscriptions WHERE status <> 'paused' AND next_refresh_at <= $1
		ORDER BY next_refresh_at LIMIT $2`
)

func scanSub(row pgx.Row) (domain.Subscription, error) {
	var s domain.Subscription
	var secs int64
	var failures int32
	err := row.Scan(&s.ID, &s.OwnerID, &s.CalendarID, &s.URLEnc, &s.Host, &secs, &s.NextRefreshAt, &s.Status, &s.ETag,
		&s.LastModified, &s.ContentHash, &failures, &s.LastAttemptAt, &s.LastOKAt, &s.LastError)
	s.Interval, s.Failures = time.Duration(secs)*time.Second, int(failures)
	return s, err
}

func (s *Store) CreateSubscription(ctx context.Context, sub domain.Subscription, cal domain.Calendar) (domain.Subscription, error) {
	err := s.rawTx(ctx, func(tx pgx.Tx, q *sqlc.Queries) error {
		if _, err := createCalendar(ctx, q, cal); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, insertSubSQL, sub.ID, sub.OwnerID, sub.CalendarID, sub.URLEnc, sub.Host, sub.Interval.Seconds(),
			sub.NextRefreshAt, sub.Status, sub.ETag, sub.LastModified, sub.LastAttemptAt)
		return err
	})
	return sub, mapErr(err)
}

func (s *Store) Subscription(ctx context.Context, owner, id domain.ID) (domain.Subscription, error) {
	sub, err := scanSub(s.pool.QueryRow(ctx, subSQL, id, owner))
	return sub, mapErr(err)
}

func (s *Store) SubscriptionByID(ctx context.Context, id domain.ID) (domain.Subscription, error) {
	sub, err := scanSub(s.pool.QueryRow(ctx, subByIDSQL, id))
	return sub, mapErr(err)
}

func (s *Store) Subscriptions(ctx context.Context, owner domain.ID) ([]domain.Subscription, error) {
	rows, err := s.pool.Query(ctx, subsSQL, owner)
	if err != nil {
		return nil, mapErr(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Subscription, error) { return scanSub(r) })
	return out, mapErr(err)
}

func (s *Store) SaveSubscriptionState(ctx context.Context, sub domain.Subscription) error {
	_, err := s.pool.Exec(ctx, saveSubSQL, sub.ID, sub.NextRefreshAt, sub.Status, sub.ETag, sub.LastModified,
		sub.ContentHash, int32(sub.Failures), sub.LastAttemptAt, sub.LastOKAt, sub.LastError)
	return mapErr(err)
}

func (s *Store) DueSubscriptions(ctx context.Context, now time.Time, limit int) ([]domain.ID, error) {
	rows, err := s.pool.Query(ctx, dueSubsSQL, now, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[domain.ID])
	return out, mapErr(err)
}
