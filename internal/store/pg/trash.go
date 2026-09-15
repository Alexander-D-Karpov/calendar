package pg

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Alexander-D-Karpov/calendar/internal/db/sqlc"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const (
	trashEventsSQL = `SELECT e.id, e.title, c.name, e.all_day, e.start_at, e.start_date, e.deleted_at,
		(SELECT count(*) FROM events o WHERE o.series_id = e.id AND o.deleted_at = e.deleted_at)
	FROM events e JOIN calendars c ON c.id = e.calendar_id
	WHERE e.owner_id = $1 AND e.deleted_at IS NOT NULL AND e.series_id IS NULL
	ORDER BY e.deleted_at DESC, e.id LIMIT $2`

	trashTodosSQL = `SELECT t.id, t.title, l.name, t.due_date, t.deleted_at,
		(SELECT count(*) FROM todos s WHERE s.parent_id = t.id AND s.deleted_at = t.deleted_at)
	FROM todos t JOIN todo_lists l ON l.id = t.list_id
	WHERE t.owner_id = $1 AND t.deleted_at IS NOT NULL AND t.parent_id IS NULL
	ORDER BY t.deleted_at DESC, t.id LIMIT $2`

	restoreEventSQL = `UPDATE events SET deleted_at = NULL, version = version + 1, updated_at = now()
		WHERE owner_id = $1 AND (id = $2 OR series_id = $2) AND deleted_at = $3
		RETURNING ` + eventColumns
	restoreTodoSQL = `UPDATE todos SET deleted_at = NULL, version = version + 1, updated_at = now()
		WHERE owner_id = $1 AND (id = $2 OR parent_id = $2) AND deleted_at = $3
		RETURNING ` + todoColumns
	deletedAtSQL = `SELECT deleted_at FROM %s WHERE owner_id = $1 AND id = $2 AND deleted_at IS NOT NULL`

	purgeEventSQL = `DELETE FROM events WHERE owner_id = $1 AND (id = $2 OR series_id = $2) AND deleted_at IS NOT NULL`
	purgeTodoSQL  = `DELETE FROM todos WHERE owner_id = $1 AND (id = $2 OR parent_id = $2) AND deleted_at IS NOT NULL`
)

func (s *Store) Trash(ctx context.Context, owner domain.ID, limit int) ([]domain.TrashItem, error) {
	out := []domain.TrashItem{}
	rows, err := s.pool.Query(ctx, trashEventsSQL, owner, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	for rows.Next() {
		var (
			it      domain.TrashItem
			allDay  bool
			startAt *time.Time
			date    pgtype.Date
			n       int64
		)
		if err := rows.Scan(&it.ID, &it.Title, &it.Where, &allDay, &startAt, &date, &it.DeletedAt, &n); err != nil {
			rows.Close()
			return nil, mapErr(err)
		}
		it.Entity, it.Children = domain.EntityEvent, int(n)
		switch {
		case allDay && date.Valid:
			it.When = date.Time.Format(domain.DateLayout)
		case startAt != nil:
			it.When = startAt.UTC().Format("2006-01-02 15:04")
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	rows, err = s.pool.Query(ctx, trashTodosSQL, owner, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			it   domain.TrashItem
			date pgtype.Date
			n    int64
		)
		if err := rows.Scan(&it.ID, &it.Title, &it.Where, &date, &it.DeletedAt, &n); err != nil {
			return nil, mapErr(err)
		}
		it.Entity, it.Children = domain.EntityTodo, int(n)
		if date.Valid {
			it.When = date.Time.Format(domain.DateLayout)
		}
		out = append(out, it)
	}
	return out, mapErr(rows.Err())
}

func (s *Store) Restore(ctx context.Context, owner, id domain.ID, entity string) error {
	return mapErr(s.rawTx(ctx, func(tx pgx.Tx, q *sqlc.Queries) error {
		table, sql := "events", restoreEventSQL
		if entity == domain.EntityTodo {
			table, sql = "todos", restoreTodoSQL
		}
		var at time.Time
		err := tx.QueryRow(ctx, "SELECT deleted_at FROM "+table+" WHERE owner_id = $1 AND id = $2 AND deleted_at IS NOT NULL", owner, id).Scan(&at)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, sql, owner, id, at)
		if err != nil {
			return err
		}
		defer rows.Close()
		n := 0
		for rows.Next() {
			n++
			if entity == domain.EntityTodo {
				t, err := scanTodo(rows)
				if err != nil {
					return err
				}
				list := t.ListID
				// Without the span a restored todo never overlaps an open view's
				// range, so the undo lands in the database but no calendar
				// refreshes. Every other todo mutation records it.
				from, to := todoRange(t)
				if err := recordChange(ctx, q, domain.Change{
					OwnerID: owner, Entity: domain.EntityTodo, EntityID: t.ID, Op: domain.OpRestore,
					ListID: &list, From: from, To: to,
				}); err != nil {
					return err
				}
				continue
			}
			e, err := scanEvent(rows)
			if err != nil {
				return err
			}
			cal := e.CalendarID
			from, to := eventSpan(e)
			if err := recordChange(ctx, q, domain.Change{
				OwnerID: owner, Entity: domain.EntityEvent, EntityID: e.ID, Op: domain.OpRestore,
				CalendarID: &cal, From: from, To: to,
			}); err != nil {
				return err
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if n == 0 {
			return domain.ErrNotFound
		}
		return nil
	}))
}

func (s *Store) PurgeItem(ctx context.Context, owner, id domain.ID, entity string) error {
	sql := purgeEventSQL
	if entity == domain.EntityTodo {
		sql = purgeTodoSQL
	}
	tag, err := s.pool.Exec(ctx, sql, owner, id)
	return affected(tag.RowsAffected(), err)
}
