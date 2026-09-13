package pg

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Alexander-D-Karpov/calendar/internal/db/sqlc"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/store"
)

const dupColumns = `id, owner_id, entity, a_id, b_id, reason, score, status, coalesce(a_snapshot, 'null'::jsonb),
	coalesce(b_snapshot, 'null'::jsonb), created_at, resolved_at`

const (
	insertDupSQL = `INSERT INTO duplicate_candidates (id, owner_id, entity, a_id, b_id, reason, score, a_snapshot, b_snapshot)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (owner_id, entity, a_id, b_id) DO UPDATE SET reason = EXCLUDED.reason, score = EXCLUDED.score,
		a_snapshot = EXCLUDED.a_snapshot, b_snapshot = EXCLUDED.b_snapshot
		WHERE duplicate_candidates.status = 'pending'`
	dupSQL      = `SELECT ` + dupColumns + ` FROM duplicate_candidates WHERE id = $1 AND owner_id = $2 FOR UPDATE`
	listDupsSQL = `SELECT ` + dupColumns + ` FROM duplicate_candidates WHERE owner_id = $1 AND status = $2
		ORDER BY created_at DESC, id LIMIT $3`
	countDupsSQL   = `SELECT count(*) FROM duplicate_candidates WHERE owner_id = $1 AND status = 'pending'`
	resolveDupSQL  = `UPDATE duplicate_candidates SET status = $3, resolved_at = $4 WHERE id = $1 AND owner_id = $2 AND status = 'pending'`
	dropDupsSQL    = `DELETE FROM duplicate_candidates WHERE owner_id = $1 AND entity = $2 AND (a_id = ANY($3) OR b_id = ANY($3))`
	pendingPairSQL = `SELECT EXISTS (SELECT 1 FROM duplicate_candidates WHERE owner_id = $1 AND entity = $2
		AND a_id = least($3::uuid, $4::uuid) AND b_id = greatest($3::uuid, $4::uuid) AND status <> 'pending')`

	scanEventsSQL = `SELECT ` + eventColumns + ` FROM events
		WHERE owner_id = $1 AND deleted_at IS NULL AND series_id IS NULL AND span && tstzrange($2, $3)
		ORDER BY id`
	scanTodosSQL = `SELECT ` + todoColumns + ` FROM todos
		WHERE owner_id = $1 AND deleted_at IS NULL AND parent_id IS NULL AND status = 'needs_action'
		ORDER BY id LIMIT $2`
	similarSQL = `SELECT similarity(lower(f_unaccent($1)), lower(f_unaccent($2)))`

	ownersSQL = `SELECT id FROM users WHERE disabled_at IS NULL ORDER BY id`
)

func scanDuplicate(row pgx.Row) (domain.Duplicate, error) {
	var d domain.Duplicate
	var score float32
	err := row.Scan(&d.ID, &d.OwnerID, &d.Entity, &d.AID, &d.BID, &d.Reason, &score, &d.Status, &d.A, &d.B, &d.CreatedAt, &d.ResolvedAt)
	d.Score = float64(score)
	return d, err
}

func (s *Store) Duplicates(ctx context.Context, owner domain.ID, status string, limit int) ([]domain.Duplicate, error) {
	rows, err := s.pool.Query(ctx, listDupsSQL, owner, status, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Duplicate, error) { return scanDuplicate(r) })
	return out, mapErr(err)
}

func (s *Store) CountDuplicates(ctx context.Context, owner domain.ID) (int, error) {
	var n int64
	err := s.pool.QueryRow(ctx, countDupsSQL, owner).Scan(&n)
	return int(n), mapErr(err)
}

func (s *Store) Owners(ctx context.Context) ([]domain.ID, error) {
	rows, err := s.pool.Query(ctx, ownersSQL)
	if err != nil {
		return nil, mapErr(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[domain.ID])
	return out, mapErr(err)
}

func (s *Store) EventsInSpan(ctx context.Context, owner domain.ID, from, to time.Time) ([]domain.Event, error) {
	return listEvents(ctx, s.pool, scanEventsSQL, owner, from, to)
}

func (s *Store) OpenTodos(ctx context.Context, owner domain.ID, limit int) ([]domain.Todo, error) {
	return listTodos(ctx, s.pool, scanTodosSQL, owner, limit)
}

func (s *Store) Similarity(ctx context.Context, a, b string) (float64, error) {
	var score float32
	err := s.pool.QueryRow(ctx, similarSQL, a, b).Scan(&score)
	return float64(score), mapErr(err)
}

func (s *Store) Resolved(ctx context.Context, owner domain.ID, entity string, a, b domain.ID) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, pendingPairSQL, owner, entity, a, b).Scan(&ok)
	return ok, mapErr(err)
}

type dedupTx struct {
	tx     pgx.Tx
	owner  domain.ID
	events *eventTx
	todos  *todoTx
}

func (s *Store) WithDedup(ctx context.Context, owner domain.ID, fn func(store.DedupTx) error) error {
	return mapErr(s.rawTx(ctx, func(tx pgx.Tx, q *sqlc.Queries) error {
		return fn(&dedupTx{
			tx:     tx,
			owner:  owner,
			events: &eventTx{tx: tx, q: q, owner: owner},
			todos:  &todoTx{tx: tx, q: q, owner: owner},
		})
	}))
}

func (t *dedupTx) Events() store.EventTx {
	return t.events
}

func (t *dedupTx) Todos() store.TodoTx {
	return t.todos
}

func (t *dedupTx) Duplicate(ctx context.Context, owner, id domain.ID) (domain.Duplicate, error) {
	d, err := scanDuplicate(t.tx.QueryRow(ctx, dupSQL, id, owner))
	return d, mapErr(err)
}

func (t *dedupTx) SaveDuplicate(ctx context.Context, d domain.Duplicate) error {
	a, b := d.AID, d.BID
	sa, sb := d.A, d.B
	if b.String() < a.String() {
		a, b, sa, sb = b, a, sb, sa
	}
	_, err := t.tx.Exec(ctx, insertDupSQL, d.ID, t.owner, d.Entity, a, b, d.Reason, float32(d.Score), sa, sb)
	return mapErr(err)
}

func (t *dedupTx) ResolveDuplicate(ctx context.Context, owner, id domain.ID, status string, at time.Time) error {
	tag, err := t.tx.Exec(ctx, resolveDupSQL, id, owner, status, at)
	return affected(tag.RowsAffected(), err)
}

func (t *dedupTx) DropDuplicates(ctx context.Context, owner domain.ID, entity string, ids ...domain.ID) error {
	_, err := t.tx.Exec(ctx, dropDupsSQL, owner, entity, ids)
	return mapErr(err)
}
