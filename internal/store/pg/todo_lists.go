package pg

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/Alexander-D-Karpov/calendar/internal/db/sqlc"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const listColumns = `id, owner_id, name, color, position, is_default, created_at, updated_at`

const (
	selectListsSQL = `SELECT ` + listColumns + ` FROM todo_lists WHERE owner_id = $1 ORDER BY position, created_at, id`
	selectListSQL  = `SELECT ` + listColumns + ` FROM todo_lists WHERE id = $1 AND owner_id = $2`
	insertListSQL  = `INSERT INTO todo_lists (id, owner_id, name, color, position) VALUES ($1, $2, $3, $4, $5) RETURNING ` + listColumns
	updateListSQL  = `UPDATE todo_lists SET name = $3, color = $4, position = $5, updated_at = now() WHERE id = $1 AND owner_id = $2 RETURNING ` + listColumns
	deleteListSQL  = `DELETE FROM todo_lists WHERE id = $1 AND owner_id = $2 AND NOT is_default`
	nextListPosSQL = `SELECT (coalesce(max(position), -1) + 1)::int FROM todo_lists WHERE owner_id = $1`
	countListsSQL  = `SELECT count(*) FROM todo_lists WHERE owner_id = $1`
)

func scanList(row pgx.Row) (domain.TodoList, error) {
	var l domain.TodoList
	var pos int32
	err := row.Scan(&l.ID, &l.OwnerID, &l.Name, &l.Color, &pos, &l.IsDefault, &l.CreatedAt, &l.UpdatedAt)
	l.Position = int(pos)
	return l, err
}

func (s *Store) ListTodoLists(ctx context.Context, owner domain.ID) ([]domain.TodoList, error) {
	rows, err := s.pool.Query(ctx, selectListsSQL, owner)
	if err != nil {
		return nil, mapErr(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.TodoList, error) { return scanList(r) })
	return out, mapErr(err)
}

func (s *Store) CountTodoLists(ctx context.Context, owner domain.ID) (int, error) {
	var n int64
	err := s.pool.QueryRow(ctx, countListsSQL, owner).Scan(&n)
	return int(n), mapErr(err)
}

func (s *Store) TodoList(ctx context.Context, owner, id domain.ID) (domain.TodoList, error) {
	l, err := scanList(s.pool.QueryRow(ctx, selectListSQL, id, owner))
	return l, mapErr(err)
}

func (s *Store) CreateTodoList(ctx context.Context, l domain.TodoList) (domain.TodoList, error) {
	var out domain.TodoList
	err := s.rawTx(ctx, func(tx pgx.Tx, q *sqlc.Queries) error {
		var err error
		out, err = createTodoList(ctx, tx, q, l)
		return err
	})
	return out, mapErr(err)
}

func createTodoList(ctx context.Context, tx pgx.Tx, q *sqlc.Queries, l domain.TodoList) (domain.TodoList, error) {
	if l.Position < 0 {
		var next int32
		if err := tx.QueryRow(ctx, nextListPosSQL, l.OwnerID).Scan(&next); err != nil {
			return domain.TodoList{}, err
		}
		l.Position = int(next)
	}
	out, err := scanList(tx.QueryRow(ctx, insertListSQL, l.ID, l.OwnerID, l.Name, l.Color, int32(l.Position)))
	if err != nil {
		return domain.TodoList{}, err
	}
	return out, recordChange(ctx, q, listChange(out, domain.OpCreate))
}

func (s *Store) UpdateTodoList(ctx context.Context, owner, id domain.ID, fn func(*domain.TodoList) error) (domain.TodoList, error) {
	var out domain.TodoList
	err := s.rawTx(ctx, func(tx pgx.Tx, q *sqlc.Queries) error {
		l, err := scanList(tx.QueryRow(ctx, selectListSQL+` FOR UPDATE`, id, owner))
		if err != nil {
			return err
		}
		if err := fn(&l); err != nil {
			return err
		}
		if out, err = scanList(tx.QueryRow(ctx, updateListSQL, id, owner, l.Name, l.Color, int32(l.Position))); err != nil {
			return err
		}
		return recordChange(ctx, q, listChange(out, domain.OpUpdate))
	})
	return out, mapErr(err)
}

func (s *Store) DeleteTodoList(ctx context.Context, owner, id domain.ID, fn func(domain.TodoList) error) error {
	return mapErr(s.rawTx(ctx, func(tx pgx.Tx, q *sqlc.Queries) error {
		l, err := scanList(tx.QueryRow(ctx, selectListSQL+` FOR UPDATE`, id, owner))
		if err != nil {
			return err
		}
		if err := fn(l); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, deleteListSQL, id, owner)
		if err := affected(tag.RowsAffected(), err); err != nil {
			return err
		}
		return recordChange(ctx, q, listChange(l, domain.OpDelete))
	}))
}

func listChange(l domain.TodoList, op string) domain.Change {
	id := l.ID
	return domain.Change{OwnerID: l.OwnerID, Entity: domain.EntityList, EntityID: id, Op: op, ListID: &id}
}
