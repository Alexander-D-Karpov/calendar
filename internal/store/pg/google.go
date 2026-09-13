package pg

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const accountColumns = `a.id, a.user_id, a.identity_id, i.email, a.refresh_token_enc, a.scopes, a.status,
	coalesce(a.last_error, ''), a.created_at, a.updated_at`

const bindingColumns = `id, owner_id, account_id, entity, local_calendar_id, local_list_id, remote_id, remote_name,
	remote_access, direction, enabled, coalesce(sync_token, ''), updated_min, coalesce(watch_channel_id, ''),
	coalesce(watch_resource_id, ''), watch_token_hash, watch_expires_at, next_poll_at, last_synced_at, coalesce(last_error, '')`

const (
	accountFrom      = ` FROM google_accounts a JOIN user_identities i ON i.id = a.identity_id`
	accountByUserSQL = `SELECT ` + accountColumns + accountFrom + ` WHERE a.user_id = $1`
	accountByIDSQL   = `SELECT ` + accountColumns + accountFrom + ` WHERE a.id = $1`
	saveAccountSQL   = `INSERT INTO google_accounts (id, user_id, identity_id, refresh_token_enc, scopes) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id) DO UPDATE SET identity_id = EXCLUDED.identity_id, refresh_token_enc = EXCLUDED.refresh_token_enc,
		scopes = EXCLUDED.scopes, status = 'ok', last_error = NULL, revoked_notified_at = NULL, updated_at = now()`
	accountStatusSQL = `UPDATE google_accounts SET status = $2, last_error = NULLIF($3, ''), updated_at = now() WHERE id = $1`
	deleteAccountSQL = `DELETE FROM google_accounts WHERE user_id = $1`

	bindingsSQL         = `SELECT ` + bindingColumns + ` FROM sync_bindings WHERE owner_id = $1 ORDER BY entity, remote_name, id`
	bindingSQL          = `SELECT ` + bindingColumns + ` FROM sync_bindings WHERE id = $1`
	bindingByChannelSQL = `SELECT ` + bindingColumns + ` FROM sync_bindings WHERE watch_channel_id = $1`
	insertBindingSQL    = `INSERT INTO sync_bindings (id, owner_id, account_id, entity, local_calendar_id, local_list_id,
		remote_id, remote_name, remote_access, direction) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING ` + bindingColumns
	updateBindingSQL = `UPDATE sync_bindings SET direction = $3, enabled = $4, updated_at = now()
		WHERE id = $1 AND owner_id = $2 RETURNING ` + bindingColumns
	deleteBindingSQL = `DELETE FROM sync_bindings WHERE id = $1 AND owner_id = $2 RETURNING ` + bindingColumns
	bindingStateSQL  = `UPDATE sync_bindings SET sync_token = NULLIF($2, ''), updated_min = $3, next_poll_at = $4,
		last_synced_at = $5, last_error = NULLIF($6, ''), updated_at = now() WHERE id = $1`
	bindingWatchSQL = `UPDATE sync_bindings SET watch_channel_id = NULLIF($2, ''), watch_resource_id = NULLIF($3, ''),
		watch_token_hash = $4, watch_expires_at = $5, updated_at = now() WHERE id = $1`
	activeAccounts  = `account_id IN (SELECT id FROM google_accounts WHERE status = 'ok')`
	dueBindingsSQL  = `SELECT id FROM sync_bindings WHERE enabled AND next_poll_at <= $1 AND ` + activeAccounts + ` ORDER BY next_poll_at LIMIT $2`
	watchTargetsSQL = `SELECT ` + bindingColumns + ` FROM sync_bindings WHERE enabled AND entity = 'calendar' AND direction <> 'push'
		AND (watch_expires_at IS NULL OR watch_expires_at < $1) AND ` + activeAccounts

	pendingOutboxSQL = `SELECT id FROM sync_outbox WHERE binding_id = $1 AND next_attempt_at <= now() ORDER BY id LIMIT $2`
	nextOutboxSQL    = `SELECT min(next_attempt_at) FROM sync_outbox WHERE binding_id = $1`
	outboxCountsSQL  = `SELECT o.binding_id, count(*) FROM sync_outbox o JOIN sync_bindings b ON b.id = o.binding_id
		WHERE b.owner_id = $1 GROUP BY o.binding_id`
	seedEventsSQL = `INSERT INTO sync_outbox (binding_id, entity, local_id, op)
		SELECT $1, 'event', id, 'upsert' FROM events WHERE calendar_id = $2 AND deleted_at IS NULL AND NOT read_only
		ORDER BY series_id NULLS FIRST, id ON CONFLICT (binding_id, entity, local_id) DO NOTHING`
	seedTodosSQL = `INSERT INTO sync_outbox (binding_id, entity, local_id, op)
		SELECT $1, 'todo', id, 'upsert' FROM todos WHERE list_id = $2 AND deleted_at IS NULL
		ORDER BY parent_id NULLS FIRST, id ON CONFLICT (binding_id, entity, local_id) DO NOTHING`
)

func scanAccount(row pgx.Row) (domain.GoogleAccount, error) {
	var a domain.GoogleAccount
	err := row.Scan(&a.ID, &a.UserID, &a.IdentityID, &a.Email, &a.RefreshTokenEnc, &a.Scopes, &a.Status, &a.LastError, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

func scanBinding(row pgx.Row) (domain.SyncBinding, error) {
	var b domain.SyncBinding
	err := row.Scan(&b.ID, &b.OwnerID, &b.AccountID, &b.Entity, &b.CalendarID, &b.ListID, &b.RemoteID, &b.RemoteName,
		&b.RemoteAccess, &b.Direction, &b.Enabled, &b.SyncToken, &b.UpdatedMin, &b.WatchChannelID, &b.WatchResourceID,
		&b.WatchTokenHash, &b.WatchExpiresAt, &b.NextPollAt, &b.LastSyncedAt, &b.LastError)
	return b, err
}

func (s *Store) GoogleAccount(ctx context.Context, user domain.ID) (domain.GoogleAccount, error) {
	a, err := scanAccount(s.pool.QueryRow(ctx, accountByUserSQL, user))
	return a, mapErr(err)
}

func (s *Store) GoogleAccountByID(ctx context.Context, id domain.ID) (domain.GoogleAccount, error) {
	a, err := scanAccount(s.pool.QueryRow(ctx, accountByIDSQL, id))
	return a, mapErr(err)
}

func (s *Store) SaveGoogleAccount(ctx context.Context, a domain.GoogleAccount) (domain.GoogleAccount, error) {
	if _, err := s.pool.Exec(ctx, saveAccountSQL, a.ID, a.UserID, a.IdentityID, a.RefreshTokenEnc, a.Scopes); err != nil {
		return domain.GoogleAccount{}, mapErr(err)
	}
	return s.GoogleAccount(ctx, a.UserID)
}

func (s *Store) SetGoogleAccountStatus(ctx context.Context, id domain.ID, status, lastErr string) error {
	_, err := s.pool.Exec(ctx, accountStatusSQL, id, status, lastErr)
	return mapErr(err)
}

func (s *Store) DeleteGoogleAccount(ctx context.Context, user domain.ID) error {
	_, err := s.pool.Exec(ctx, deleteAccountSQL, user)
	return mapErr(err)
}

func (s *Store) collectBindings(ctx context.Context, sql string, args ...any) ([]domain.SyncBinding, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.SyncBinding, error) { return scanBinding(r) })
	return out, mapErr(err)
}

func (s *Store) Bindings(ctx context.Context, owner domain.ID) ([]domain.SyncBinding, error) {
	return s.collectBindings(ctx, bindingsSQL, owner)
}

func (s *Store) WatchCandidates(ctx context.Context, before time.Time) ([]domain.SyncBinding, error) {
	return s.collectBindings(ctx, watchTargetsSQL, before)
}

func (s *Store) Binding(ctx context.Context, id domain.ID) (domain.SyncBinding, error) {
	b, err := scanBinding(s.pool.QueryRow(ctx, bindingSQL, id))
	return b, mapErr(err)
}

func (s *Store) BindingByChannel(ctx context.Context, channel string) (domain.SyncBinding, error) {
	b, err := scanBinding(s.pool.QueryRow(ctx, bindingByChannelSQL, channel))
	return b, mapErr(err)
}

func (s *Store) CreateBinding(ctx context.Context, b domain.SyncBinding) (domain.SyncBinding, error) {
	out, err := scanBinding(s.pool.QueryRow(ctx, insertBindingSQL, b.ID, b.OwnerID, b.AccountID, b.Entity, b.CalendarID,
		b.ListID, b.RemoteID, b.RemoteName, b.RemoteAccess, b.Direction))
	return out, mapErr(err)
}

func (s *Store) UpdateBinding(ctx context.Context, owner, id domain.ID, direction string, enabled bool) (domain.SyncBinding, error) {
	out, err := scanBinding(s.pool.QueryRow(ctx, updateBindingSQL, id, owner, direction, enabled))
	return out, mapErr(err)
}

func (s *Store) DeleteBinding(ctx context.Context, owner, id domain.ID) (domain.SyncBinding, error) {
	out, err := scanBinding(s.pool.QueryRow(ctx, deleteBindingSQL, id, owner))
	return out, mapErr(err)
}

func (s *Store) SaveBindingState(ctx context.Context, b domain.SyncBinding) error {
	_, err := s.pool.Exec(ctx, bindingStateSQL, b.ID, b.SyncToken, b.UpdatedMin, b.NextPollAt, b.LastSyncedAt, b.LastError)
	return mapErr(err)
}

func (s *Store) SaveWatch(ctx context.Context, id domain.ID, channel, resource string, tokenHash []byte, expires *time.Time) error {
	if channel == "" {
		tokenHash, expires = nil, nil
	}
	_, err := s.pool.Exec(ctx, bindingWatchSQL, id, channel, resource, tokenHash, expires)
	return mapErr(err)
}

func (s *Store) DueBindings(ctx context.Context, now time.Time, limit int) ([]domain.ID, error) {
	rows, err := s.pool.Query(ctx, dueBindingsSQL, now, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[domain.ID])
	return out, mapErr(err)
}

func (s *Store) SeedOutbox(ctx context.Context, b domain.SyncBinding) error {
	sql, local := seedEventsSQL, b.CalendarID
	if b.Entity == domain.BindTaskList {
		sql, local = seedTodosSQL, b.ListID
	}
	if local == nil {
		return nil
	}
	_, err := s.pool.Exec(ctx, sql, b.ID, *local)
	return mapErr(err)
}

func (s *Store) PendingOutbox(ctx context.Context, binding domain.ID, limit int) ([]int64, error) {
	rows, err := s.pool.Query(ctx, pendingOutboxSQL, binding, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	return out, mapErr(err)
}

func (s *Store) NextOutboxAt(ctx context.Context, binding domain.ID) (*time.Time, error) {
	var at *time.Time
	err := s.pool.QueryRow(ctx, nextOutboxSQL, binding).Scan(&at)
	return at, mapErr(err)
}

func (s *Store) OutboxCounts(ctx context.Context, owner domain.ID) (map[domain.ID]int, error) {
	rows, err := s.pool.Query(ctx, outboxCountsSQL, owner)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := map[domain.ID]int{}
	for rows.Next() {
		var id domain.ID
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			return nil, mapErr(err)
		}
		out[id] = int(n)
	}
	return out, mapErr(rows.Err())
}

const listConflictsSQL = `SELECT owner_id, binding_id, entity, local_id, remote_id, winner,
	local_snapshot, remote_snapshot, created_at
	FROM sync_conflicts WHERE owner_id = $1 ORDER BY created_at DESC LIMIT $2`

func (s *Store) Conflicts(ctx context.Context, owner domain.ID, limit int) ([]domain.SyncConflict, error) {
	rows, err := s.pool.Query(ctx, listConflictsSQL, owner, limit)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []domain.SyncConflict
	for rows.Next() {
		var (
			c             domain.SyncConflict
			binding       *domain.ID
			local, remote []byte
		)
		if err := rows.Scan(&c.OwnerID, &binding, &c.Entity, &c.LocalID, &c.RemoteID, &c.Winner, &local, &remote, &c.CreatedAt); err != nil {
			return nil, mapErr(err)
		}
		if binding != nil {
			c.BindingID = *binding
		}
		_ = json.Unmarshal(local, &c.Local)
		_ = json.Unmarshal(remote, &c.Remote)
		out = append(out, c)
	}
	return out, mapErr(rows.Err())
}
