package pg

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Alexander-D-Karpov/calendar/internal/db/sqlc"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

func recordChange(ctx context.Context, q *sqlc.Queries, c domain.Change) error {
	origin := c.Origin
	if origin == "" {
		origin = domain.OriginFrom(ctx)
	}
	_, err := q.InsertChange(ctx, sqlc.InsertChangeParams{
		OwnerID:    c.OwnerID,
		Entity:     c.Entity,
		EntityID:   c.EntityID,
		Op:         c.Op,
		CalendarID: c.CalendarID,
		ListID:     c.ListID,
		Span:       tstzrange(c.From, c.To),
		Origin:     textPtr(origin),
	})
	return err
}

func tstzrange(from, to *time.Time) pgtype.Range[pgtype.Timestamptz] {
	if from == nil {
		return pgtype.Range[pgtype.Timestamptz]{}
	}
	r := pgtype.Range[pgtype.Timestamptz]{
		Lower:     pgtype.Timestamptz{Time: *from, Valid: true},
		LowerType: pgtype.Inclusive,
		UpperType: pgtype.Unbounded,
		Valid:     true,
	}
	if to != nil {
		r.Upper = pgtype.Timestamptz{Time: *to, Valid: true}
		r.UpperType = pgtype.Exclusive
		if !to.After(*from) {
			r.UpperType = pgtype.Inclusive
		}
	}
	return r
}

func rangeBounds(r pgtype.Range[pgtype.Timestamptz]) (*time.Time, *time.Time) {
	var from, to *time.Time
	if r.Lower.Valid {
		t := r.Lower.Time
		from = &t
	}
	if r.Upper.Valid && r.UpperType != pgtype.Unbounded {
		t := r.Upper.Time
		to = &t
	}
	return from, to
}

const (
	changesSinceSQL = `SELECT seq, entity, entity_id, op, calendar_id, list_id, span, coalesce(origin, '')
		FROM changes WHERE owner_id = $1 AND seq > $2 ORDER BY seq LIMIT $3`
	latestSeqSQL = `SELECT coalesce(max(seq), 0) FROM changes WHERE owner_id = $1`
)

func (s *Store) LatestSeq(ctx context.Context, owner domain.ID) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, latestSeqSQL, owner).Scan(&n)
	return n, mapErr(err)
}

func (s *Store) ChangesSince(ctx context.Context, owner domain.ID, since int64, limit int) ([]domain.Change, error) {
	rows, err := s.pool.Query(ctx, changesSinceSQL, owner, since, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []domain.Change
	for rows.Next() {
		c := domain.Change{OwnerID: owner}
		var span pgtype.Range[pgtype.Timestamptz]
		if err := rows.Scan(&c.Seq, &c.Entity, &c.EntityID, &c.Op, &c.CalendarID, &c.ListID, &span, &c.Origin); err != nil {
			return nil, mapErr(err)
		}
		c.From, c.To = rangeBounds(span)
		out = append(out, c)
	}
	return out, mapErr(rows.Err())
}
