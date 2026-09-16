package pg

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Alexander-D-Karpov/calendar/internal/db"
	"github.com/Alexander-D-Karpov/calendar/internal/db/sqlc"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/jobs"
	"github.com/Alexander-D-Karpov/calendar/internal/store"
)

const syncLockID = 7261004

const mappingColumns = `binding_id, entity, local_id, remote_id, coalesce(remote_etag, ''), remote_updated,
	local_version_at_sync, remote_due_date`

const (
	syncLockSQL      = `SELECT pg_advisory_xact_lock($1, hashtext($2))`
	mappingSQL       = `SELECT ` + mappingColumns + ` FROM sync_mappings WHERE binding_id = $1 AND entity = $2 AND local_id = $3`
	mappingRemoteSQL = `SELECT ` + mappingColumns + ` FROM sync_mappings WHERE binding_id = $1 AND entity = $2 AND remote_id = $3`
	mappingsSQL      = `SELECT ` + mappingColumns + ` FROM sync_mappings WHERE binding_id = $1 AND entity = $2`
	releaseRemoteSQL = `DELETE FROM sync_mappings WHERE binding_id = $1 AND entity = $2 AND remote_id = $3 AND local_id <> $4`
	saveMappingSQL   = `INSERT INTO sync_mappings (binding_id, entity, local_id, remote_id, remote_etag, remote_updated,
		local_version_at_sync, remote_due_date, synced_at) VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, now())
		ON CONFLICT (binding_id, entity, local_id) DO UPDATE SET remote_id = EXCLUDED.remote_id, remote_etag = EXCLUDED.remote_etag,
		remote_updated = EXCLUDED.remote_updated, local_version_at_sync = EXCLUDED.local_version_at_sync,
		remote_due_date = EXCLUDED.remote_due_date, synced_at = now()`
	deleteMappingSQL = `DELETE FROM sync_mappings WHERE binding_id = $1 AND entity = $2 AND local_id = $3`
	deletePrefixSQL  = `DELETE FROM sync_mappings WHERE binding_id = $1 AND entity = $2 AND starts_with(remote_id, $3)`
	uidTakenSQL      = `SELECT EXISTS (SELECT 1 FROM events WHERE calendar_id = $1 AND ical_uid = $2 AND series_id IS NULL AND deleted_at IS NULL)`
	claimOutboxSQL   = `SELECT id, binding_id, entity, local_id, op, coalesce(remote_id, ''), attempts FROM sync_outbox
		WHERE id = $1 AND binding_id = $2 FOR UPDATE SKIP LOCKED`
	finishOutboxSQL   = `DELETE FROM sync_outbox WHERE id = $1`
	retryOutboxSQL    = `UPDATE sync_outbox SET attempts = $2, next_attempt_at = $3, last_error = $4, updated_at = now() WHERE id = $1`
	dropOutboxSQL     = `DELETE FROM sync_outbox WHERE binding_id = $1 AND entity = $2 AND local_id = $3`
	pendingOpSQL      = `SELECT op FROM sync_outbox WHERE binding_id = $1 AND entity = $2 AND local_id = $3`
	insertConflictSQL = `INSERT INTO sync_conflicts (id, owner_id, binding_id, entity, local_id, remote_id, winner, local_snapshot, remote_snapshot)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	queueSyncSQL = `WITH b AS (
		SELECT b.id, m.remote_id FROM sync_bindings b
		LEFT JOIN sync_mappings m ON m.binding_id = b.id AND m.entity = $2::text AND m.local_id = $3::uuid
		WHERE b.owner_id = $1::uuid AND b.enabled AND b.direction IN ('both', 'push')
			AND (b.local_calendar_id = $4::uuid OR b.local_list_id = $5::uuid)
	), ins AS (
		INSERT INTO sync_outbox (binding_id, entity, local_id, op, remote_id)
		SELECT id, $2::text, $3::uuid, $6::text, remote_id FROM b WHERE $6::text = 'upsert' OR remote_id IS NOT NULL
		ON CONFLICT (binding_id, entity, local_id) DO UPDATE SET op = EXCLUDED.op,
			remote_id = coalesce(EXCLUDED.remote_id, sync_outbox.remote_id), attempts = 0,
			next_attempt_at = now(), last_error = NULL, updated_at = now()
		RETURNING binding_id
	), dropped AS (
		DELETE FROM sync_outbox o USING b
		WHERE $6::text = 'delete' AND b.remote_id IS NULL AND o.binding_id = b.id AND o.entity = $2::text AND o.local_id = $3::uuid
	)
	INSERT INTO jobs (kind, payload, unique_key)
	SELECT $7::text, json_build_object('binding', binding_id), binding_id::text FROM ins
	ON CONFLICT (kind, unique_key) WHERE unique_key IS NOT NULL AND status = 'queued' DO NOTHING`
)

func queueSync(ctx context.Context, tx pgx.Tx, owner domain.ID, entity string, id domain.ID, op string, cal, list *domain.ID) error {
	if domain.OriginFrom(ctx) == domain.OriginGoogle {
		return nil
	}
	syncOp := domain.SyncUpsert
	if op == domain.OpDelete {
		syncOp = domain.SyncDelete
	}
	_, err := tx.Exec(ctx, queueSyncSQL, owner, entity, id, cal, list, syncOp, jobs.KindGooglePush)
	return err
}

type syncTx struct {
	tx      pgx.Tx
	binding domain.ID
	owner   domain.ID
	events  *eventTx
	todos   *todoTx
}

func (s *Store) WithSync(ctx context.Context, owner, binding domain.ID, fn func(store.SyncTx) error) error {
	return mapErr(s.rawTx(ctx, func(tx pgx.Tx, q *sqlc.Queries) error {
		if _, err := tx.Exec(ctx, syncLockSQL, syncLockID, binding.String()); err != nil {
			return err
		}
		return fn(&syncTx{
			tx:      tx,
			binding: binding,
			owner:   owner,
			events:  &eventTx{tx: tx, q: q, owner: owner},
			todos:   &todoTx{tx: tx, q: q, owner: owner},
		})
	}))
}

func (t *syncTx) Events() store.EventTx {
	return t.events
}

func (t *syncTx) Todos() store.TodoTx {
	return t.todos
}

func scanMapping(row pgx.Row) (domain.SyncMapping, error) {
	var m domain.SyncMapping
	var due pgtype.Date
	err := row.Scan(&m.BindingID, &m.Entity, &m.LocalID, &m.RemoteID, &m.RemoteETag, &m.RemoteUpdated, &m.LocalVersion, &due)
	if due.Valid {
		d := due.Time.UTC()
		m.RemoteDueDate = &d
	}
	return m, err
}

func optDate(t *time.Time) pgtype.Date {
	if t == nil {
		return pgtype.Date{}
	}
	return pgDate(*t)
}

func (t *syncTx) Mapping(ctx context.Context, entity string, local domain.ID) (domain.SyncMapping, error) {
	m, err := scanMapping(t.tx.QueryRow(ctx, mappingSQL, t.binding, entity, local))
	return m, mapErr(err)
}

func (t *syncTx) MappingByRemote(ctx context.Context, entity, remote string) (domain.SyncMapping, error) {
	m, err := scanMapping(t.tx.QueryRow(ctx, mappingRemoteSQL, t.binding, entity, remote))
	return m, mapErr(err)
}

func (t *syncTx) Mappings(ctx context.Context, entity string) ([]domain.SyncMapping, error) {
	rows, err := t.tx.Query(ctx, mappingsSQL, t.binding, entity)
	if err != nil {
		return nil, mapErr(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.SyncMapping, error) { return scanMapping(r) })
	return out, mapErr(err)
}

func (t *syncTx) SaveMapping(ctx context.Context, m domain.SyncMapping) error {
	if _, err := t.tx.Exec(ctx, releaseRemoteSQL, t.binding, m.Entity, m.RemoteID, m.LocalID); err != nil {
		return mapErr(err)
	}
	_, err := t.tx.Exec(ctx, saveMappingSQL, t.binding, m.Entity, m.LocalID, m.RemoteID, m.RemoteETag, m.RemoteUpdated,
		m.LocalVersion, optDate(m.RemoteDueDate))
	return mapErr(err)
}

func (t *syncTx) DeleteMapping(ctx context.Context, entity string, local domain.ID) error {
	_, err := t.tx.Exec(ctx, deleteMappingSQL, t.binding, entity, local)
	return mapErr(err)
}

func (t *syncTx) DeleteMappingsWithPrefix(ctx context.Context, entity, prefix string) error {
	_, err := t.tx.Exec(ctx, deletePrefixSQL, t.binding, entity, prefix)
	return mapErr(err)
}

func (t *syncTx) UIDTaken(ctx context.Context, calendar domain.ID, uid string) (bool, error) {
	var ok bool
	err := t.tx.QueryRow(ctx, uidTakenSQL, calendar, uid).Scan(&ok)
	return ok, mapErr(err)
}

// EventByUID lets the puller re-link an event it already holds instead of
// inserting a second copy. UIDTaken alone cannot: it says the UID exists but
// not which event owns it.
func (t *syncTx) EventByUID(ctx context.Context, calendar domain.ID, uid string) (domain.Event, error) {
	return t.events.one(ctx, eventByUIDSQL, t.owner, calendar, uid)
}

func (t *syncTx) ClaimOutbox(ctx context.Context, id int64) (domain.OutboxItem, bool, error) {
	var it domain.OutboxItem
	var attempts int32
	err := t.tx.QueryRow(ctx, claimOutboxSQL, id, t.binding).Scan(&it.ID, &it.BindingID, &it.Entity, &it.LocalID, &it.Op, &it.RemoteID, &attempts)
	if db.IsNotFound(err) {
		return it, false, nil
	}
	it.Attempts = int(attempts)
	return it, err == nil, mapErr(err)
}

func (t *syncTx) FinishOutbox(ctx context.Context, id int64) error {
	_, err := t.tx.Exec(ctx, finishOutboxSQL, id)
	return mapErr(err)
}

func (t *syncTx) RetryOutbox(ctx context.Context, id int64, attempts int, next time.Time, lastErr string) error {
	_, err := t.tx.Exec(ctx, retryOutboxSQL, id, int32(attempts), next, lastErr)
	return mapErr(err)
}

func (t *syncTx) DropOutbox(ctx context.Context, entity string, local domain.ID) error {
	_, err := t.tx.Exec(ctx, dropOutboxSQL, t.binding, entity, local)
	return mapErr(err)
}

func (t *syncTx) PendingOp(ctx context.Context, entity string, local domain.ID) (string, bool, error) {
	var op string
	err := t.tx.QueryRow(ctx, pendingOpSQL, t.binding, entity, local).Scan(&op)
	if db.IsNotFound(err) {
		return "", false, nil
	}
	return op, err == nil, mapErr(err)
}

func (t *syncTx) Conflict(ctx context.Context, c domain.SyncConflict) error {
	local, err := json.Marshal(c.Local)
	if err != nil {
		return err
	}
	remote, err := json.Marshal(c.Remote)
	if err != nil {
		return err
	}
	_, err = t.tx.Exec(ctx, insertConflictSQL, domain.NewID(), t.owner, t.binding, c.Entity, c.LocalID, c.RemoteID, c.Winner, local, remote)
	return mapErr(err)
}
