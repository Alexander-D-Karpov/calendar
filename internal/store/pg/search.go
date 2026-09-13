package pg

import (
	"context"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/search"
)

const (
	searchTimeoutSQL = `SET LOCAL statement_timeout = '5s'`
)

func (s *Store) Search(ctx context.Context, p search.Params) ([]search.Hit, error) {
	sql, args := search.Build(p)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, searchTimeoutSQL); err != nil {
		return nil, mapErr(err)
	}
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []search.Hit
	for rows.Next() {
		var (
			h       search.Hit
			starts  *time.Time
			ends    *time.Time
			allDay  bool
			color   string
			done    bool
			snippet string
		)
		if err := rows.Scan(&h.Kind, &h.ID, &h.Title, &snippet, &starts, &ends, &allDay, &h.Container, &color, &done, &h.Rank); err != nil {
			return nil, mapErr(err)
		}
		h.Snippet, h.AllDay, h.Color, h.Done = snippet, allDay, color, done
		h.Start, h.End = starts, ends
		out = append(out, h)
	}
	return out, mapErr(rows.Err())
}
