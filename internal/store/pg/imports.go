package pg

import (
	"context"
	"encoding/hex"

	"github.com/jackc/pgx/v5"

	"github.com/Alexander-D-Karpov/calendar/internal/db"
	"github.com/Alexander-D-Karpov/calendar/internal/db/sqlc"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/store"
)

func importCols(payload string) string {
	return `id, owner_id, source, filename, status, ` + payload + `, options, coalesce(preview, 'null'::jsonb),
	coalesce(stats, 'null'::jsonb), coalesce(error, ''), created_at, updated_at, expires_at, finished_at`
}

var (
	insertImportSQL = `INSERT INTO imports (id, owner_id, source, filename, status, payload, payload_sha256, options, preview, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING ` + importCols("payload")
	importSQL      = `SELECT ` + importCols("payload") + ` FROM imports WHERE id = $1 AND owner_id = $2`
	importByIDSQL  = `SELECT ` + importCols("payload") + ` FROM imports WHERE id = $1`
	listImportsSQL = `SELECT ` + importCols("NULL::bytea") + ` FROM imports WHERE owner_id = $1 ORDER BY created_at DESC LIMIT $2`
	saveImportSQL  = `UPDATE imports SET status = $3, payload = $4, options = $5, preview = $6, stats = $7,
		error = NULLIF($8, ''), finished_at = $9, updated_at = now() WHERE id = $1 AND owner_id = $2 RETURNING ` + importCols("payload")
	claimImportSQL = `UPDATE imports SET status = 'committing', updated_at = now()
		WHERE id = $1 AND owner_id = $2 AND status = 'previewed'`
)

const (
	calendarEventsSQL = `SELECT ` + eventColumns + ` FROM events
		WHERE owner_id = $1 AND calendar_id = $2 AND deleted_at IS NULL ORDER BY series_id NULLS FIRST, id`
	eventByUIDSQL = `SELECT ` + eventColumns + ` FROM events WHERE owner_id = $1 AND calendar_id = $2 AND ical_uid = $3
		AND series_id IS NULL AND deleted_at IS NULL FOR UPDATE`
	eventFPSQL = `SELECT fingerprint FROM events WHERE owner_id = $1 AND calendar_id = $2 AND fingerprint = ANY($3)
		AND series_id IS NULL AND deleted_at IS NULL`
	todoByUIDSQL = `SELECT ` + todoColumns + ` FROM todos WHERE owner_id = $1 AND list_id = $2 AND ical_uid = $3
		AND parent_id IS NULL AND deleted_at IS NULL FOR UPDATE`
	todoFPSQL = `SELECT fingerprint FROM todos WHERE owner_id = $1 AND list_id = $2 AND fingerprint = ANY($3)
		AND parent_id IS NULL AND deleted_at IS NULL`

	unprintedEventsSQL = `SELECT ` + eventColumns + ` FROM events WHERE fingerprint IS NULL AND deleted_at IS NULL LIMIT $1`
	unprintedTodosSQL  = `SELECT ` + todoColumns + ` FROM todos WHERE fingerprint IS NULL AND deleted_at IS NULL LIMIT $1`
	setEventFPSQL      = `UPDATE events SET fingerprint = $2 WHERE id = $1`
	setTodoFPSQL       = `UPDATE todos SET fingerprint = $2 WHERE id = $1`
)

func scanImport(row pgx.Row) (domain.Import, error) {
	var im domain.Import
	err := row.Scan(&im.ID, &im.OwnerID, &im.Source, &im.Filename, &im.Status, &im.Payload, &im.Options, &im.Preview,
		&im.Stats, &im.Error, &im.CreatedAt, &im.UpdatedAt, &im.ExpiresAt, &im.FinishedAt)
	return im, err
}

func jsonArg(b []byte) []byte {
	if len(b) == 0 {
		return []byte("{}")
	}
	return b
}

func (s *Store) CreateImport(ctx context.Context, im domain.Import) (domain.Import, error) {
	out, err := scanImport(s.pool.QueryRow(ctx, insertImportSQL, im.ID, im.OwnerID, im.Source, im.Filename, im.Status,
		im.Payload, im.PayloadSum, jsonArg(im.Options), im.Preview, im.ExpiresAt))
	return out, mapErr(err)
}

func (s *Store) Import(ctx context.Context, owner, id domain.ID) (domain.Import, error) {
	im, err := scanImport(s.pool.QueryRow(ctx, importSQL, id, owner))
	return im, mapErr(err)
}

func (s *Store) ImportByID(ctx context.Context, id domain.ID) (domain.Import, error) {
	im, err := scanImport(s.pool.QueryRow(ctx, importByIDSQL, id))
	return im, mapErr(err)
}

func (s *Store) ListImports(ctx context.Context, owner domain.ID, limit int) ([]domain.Import, error) {
	rows, err := s.pool.Query(ctx, listImportsSQL, owner, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Import, error) { return scanImport(r) })
	return out, mapErr(err)
}

func (s *Store) SaveImport(ctx context.Context, im domain.Import) (domain.Import, error) {
	var out domain.Import
	err := s.rawTx(ctx, func(tx pgx.Tx, q *sqlc.Queries) error {
		var err error
		out, err = scanImport(tx.QueryRow(ctx, saveImportSQL, im.ID, im.OwnerID, im.Status, im.Payload, jsonArg(im.Options),
			im.Preview, im.Stats, im.Error, im.FinishedAt))
		if err != nil {
			return err
		}
		return recordChange(ctx, q, domain.Change{OwnerID: im.OwnerID, Entity: domain.EntityImport, EntityID: im.ID, Op: domain.OpUpdate})
	})
	return out, mapErr(err)
}

func (s *Store) ClaimImport(ctx context.Context, owner, id domain.ID) error {
	return mapErr(s.rawTx(ctx, func(tx pgx.Tx, q *sqlc.Queries) error {
		tag, err := tx.Exec(ctx, claimImportSQL, id, owner)
		if err := affected(tag.RowsAffected(), err); err != nil {
			return err
		}
		return recordChange(ctx, q, domain.Change{OwnerID: owner, Entity: domain.EntityImport, EntityID: id, Op: domain.OpUpdate})
	}))
}

func (s *Store) CalendarEvents(ctx context.Context, owner, calendar domain.ID) ([]domain.Event, error) {
	return listEvents(ctx, s.pool, calendarEventsSQL, owner, calendar)
}

func (s *Store) BackfillFingerprints(ctx context.Context, limit int) (int, error) {
	n := 0
	rows, err := s.pool.Query(ctx, unprintedEventsSQL, limit)
	if err != nil {
		return 0, mapErr(err)
	}
	events, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Event, error) { return scanEvent(r) })
	if err != nil {
		return 0, mapErr(err)
	}
	for _, e := range events {
		if _, err := s.pool.Exec(ctx, setEventFPSQL, e.ID, domain.EventFingerprint(e)); err != nil {
			return n, mapErr(err)
		}
		n++
	}
	rows, err = s.pool.Query(ctx, unprintedTodosSQL, limit)
	if err != nil {
		return n, mapErr(err)
	}
	todos, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Todo, error) { return scanTodo(r) })
	if err != nil {
		return n, mapErr(err)
	}
	for _, t := range todos {
		if _, err := s.pool.Exec(ctx, setTodoFPSQL, t.ID, domain.TodoFingerprint(t)); err != nil {
			return n, mapErr(err)
		}
		n++
	}
	return n, nil
}

type importTx struct {
	tx     pgx.Tx
	q      *sqlc.Queries
	owner  domain.ID
	events *eventTx
	todos  *todoTx
}

func (s *Store) WithImport(ctx context.Context, owner domain.ID, fn func(store.ImportTx) error) error {
	return mapErr(s.rawTx(ctx, func(tx pgx.Tx, q *sqlc.Queries) error {
		return fn(&importTx{
			tx:     tx,
			q:      q,
			owner:  owner,
			events: &eventTx{tx: tx, q: q, owner: owner},
			todos:  &todoTx{tx: tx, q: q, owner: owner},
		})
	}))
}

func (t *importTx) Events() store.EventTx {
	return t.events
}

func (t *importTx) Todos() store.TodoTx {
	return t.todos
}

func (t *importTx) CreateCalendar(ctx context.Context, c domain.Calendar) (domain.Calendar, error) {
	out, err := createCalendar(ctx, t.q, c)
	return out, mapErr(err)
}

func (t *importTx) CreateTodoList(ctx context.Context, l domain.TodoList) (domain.TodoList, error) {
	out, err := createTodoList(ctx, t.tx, t.q, l)
	return out, mapErr(err)
}

func (t *importTx) CalendarEvents(ctx context.Context, calendar domain.ID) ([]domain.Event, error) {
	return listEvents(ctx, t.tx, calendarEventsSQL, t.owner, calendar)
}

func (t *importTx) EventByUID(ctx context.Context, calendar domain.ID, uid string) (domain.Event, error) {
	return t.events.one(ctx, eventByUIDSQL, t.owner, calendar, uid)
}

func (t *importTx) TodoByUID(ctx context.Context, list domain.ID, uid string) (domain.Todo, error) {
	return oneTodo(listTodos(ctx, t.tx, todoByUIDSQL, t.owner, list, uid))
}

func (t *importTx) EventFingerprints(ctx context.Context, calendar domain.ID, fps [][]byte) (map[string]bool, error) {
	return fingerprintSet(ctx, t.tx, eventFPSQL, t.owner, calendar, fps)
}

func (t *importTx) TodoFingerprints(ctx context.Context, list domain.ID, fps [][]byte) (map[string]bool, error) {
	return fingerprintSet(ctx, t.tx, todoFPSQL, t.owner, list, fps)
}

func fingerprintSet(ctx context.Context, q db.DBTX, sql string, owner, parent domain.ID, fps [][]byte) (map[string]bool, error) {
	out := map[string]bool{}
	if len(fps) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, sql, owner, parent, fps)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, mapErr(err)
		}
		out[hex.EncodeToString(b)] = true
	}
	return out, mapErr(rows.Err())
}
