package pg

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Alexander-D-Karpov/calendar/internal/db/sqlc"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const shareColumns = `s.id, s.owner_id, s.name, s.token_hash, s.token_enc, s.view, s.period_date, s.detail,
	s.include_todos, s.show_sleep, s.tz_mode, s.version, s.access_count, s.last_accessed_at, s.created_at,
	s.updated_at, s.revoked_at,
	array(SELECT c.calendar_id FROM share_link_calendars c WHERE c.share_id = s.id ORDER BY c.calendar_id)`

const (
	selectSharesSQL = `SELECT ` + shareColumns + ` FROM share_links s WHERE s.owner_id = $1 ORDER BY s.created_at DESC, s.id`
	selectShareSQL  = `SELECT ` + shareColumns + ` FROM share_links s WHERE s.id = $1 AND s.owner_id = $2`
	shareByHashSQL  = `SELECT ` + shareColumns + ` FROM share_links s JOIN users u ON u.id = s.owner_id
		WHERE s.token_hash = $1 AND s.revoked_at IS NULL AND u.disabled_at IS NULL`
	countSharesSQL = `SELECT count(*) FROM share_links WHERE owner_id = $1`
	insertShareSQL = `INSERT INTO share_links (id, owner_id, name, token_hash, token_enc, view, period_date, detail,
		include_todos, show_sleep, tz_mode) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`
	updateShareSQL = `UPDATE share_links SET name = $3, token_hash = $4, token_enc = $5, detail = $6,
		include_todos = $7, show_sleep = $8, tz_mode = $9, revoked_at = $10, version = version + 1, updated_at = now()
		WHERE id = $1 AND owner_id = $2`
	clearShareCalendarsSQL  = `DELETE FROM share_link_calendars WHERE share_id = $1`
	insertShareCalendarsSQL = `INSERT INTO share_link_calendars (share_id, calendar_id, owner_id)
		SELECT $1, unnest($2::uuid[]), $3`
	touchShareSQL = `UPDATE share_links SET access_count = access_count + 1, last_accessed_at = $2 WHERE id = $1`
)

func scanShare(row pgx.Row) (domain.Share, error) {
	var (
		sh      domain.Share
		period  pgtype.Date
		version int32
	)
	err := row.Scan(&sh.ID, &sh.OwnerID, &sh.Name, &sh.TokenHash, &sh.TokenEnc, &sh.View, &period, &sh.Detail,
		&sh.IncludeTodos, &sh.ShowSleep, &sh.TZMode, &version, &sh.AccessCount, &sh.LastAccessedAt, &sh.CreatedAt,
		&sh.UpdatedAt, &sh.RevokedAt, &sh.Calendars)
	if err != nil {
		return domain.Share{}, err
	}
	sh.Period, sh.Version = period.Time.UTC(), int64(version)
	if sh.Calendars == nil {
		sh.Calendars = []domain.ID{}
	}
	return sh, nil
}

func (s *Store) ListShares(ctx context.Context, owner domain.ID) ([]domain.Share, error) {
	rows, err := s.pool.Query(ctx, selectSharesSQL, owner)
	if err != nil {
		return nil, mapErr(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Share, error) { return scanShare(r) })
	return out, mapErr(err)
}

func (s *Store) CountShares(ctx context.Context, owner domain.ID) (int, error) {
	var n int64
	err := s.pool.QueryRow(ctx, countSharesSQL, owner).Scan(&n)
	return int(n), mapErr(err)
}

func (s *Store) Share(ctx context.Context, owner, id domain.ID) (domain.Share, error) {
	sh, err := scanShare(s.pool.QueryRow(ctx, selectShareSQL, id, owner))
	return sh, mapErr(err)
}

func (s *Store) ShareByTokenHash(ctx context.Context, hash []byte) (domain.Share, error) {
	sh, err := scanShare(s.pool.QueryRow(ctx, shareByHashSQL, hash))
	return sh, mapErr(err)
}

func (s *Store) CreateShare(ctx context.Context, sh domain.Share) (domain.Share, error) {
	var out domain.Share
	err := s.rawTx(ctx, func(tx pgx.Tx, q *sqlc.Queries) error {
		_, err := tx.Exec(ctx, insertShareSQL, sh.ID, sh.OwnerID, sh.Name, sh.TokenHash, sh.TokenEnc, sh.View,
			pgDate(sh.Period), sh.Detail, sh.IncludeTodos, sh.ShowSleep, sh.TZMode)
		if err != nil {
			return err
		}
		if err := setShareCalendars(ctx, tx, sh); err != nil {
			return err
		}
		if err := recordChange(ctx, q, shareChange(sh, domain.OpCreate)); err != nil {
			return err
		}
		out, err = scanShare(tx.QueryRow(ctx, selectShareSQL, sh.ID, sh.OwnerID))
		return err
	})
	return out, mapErr(err)
}

func (s *Store) UpdateShare(ctx context.Context, owner, id domain.ID, fn func(*domain.Share) error) (domain.Share, error) {
	var out domain.Share
	err := s.rawTx(ctx, func(tx pgx.Tx, q *sqlc.Queries) error {
		sh, err := scanShare(tx.QueryRow(ctx, selectShareSQL+` FOR UPDATE OF s`, id, owner))
		if err != nil {
			return err
		}
		if err := fn(&sh); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, updateShareSQL, id, owner, sh.Name, sh.TokenHash, sh.TokenEnc, sh.Detail,
			sh.IncludeTodos, sh.ShowSleep, sh.TZMode, sh.RevokedAt)
		if err := affected(tag.RowsAffected(), err); err != nil {
			return err
		}
		if err := setShareCalendars(ctx, tx, sh); err != nil {
			return err
		}
		if err := recordChange(ctx, q, shareChange(sh, domain.OpUpdate)); err != nil {
			return err
		}
		out, err = scanShare(tx.QueryRow(ctx, selectShareSQL, id, owner))
		return err
	})
	return out, mapErr(err)
}

func (s *Store) TouchShare(ctx context.Context, id domain.ID, at time.Time) error {
	_, err := s.pool.Exec(ctx, touchShareSQL, id, at)
	return mapErr(err)
}

func setShareCalendars(ctx context.Context, tx pgx.Tx, sh domain.Share) error {
	if _, err := tx.Exec(ctx, clearShareCalendarsSQL, sh.ID); err != nil {
		return err
	}
	if len(sh.Calendars) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, insertShareCalendarsSQL, sh.ID, sh.Calendars, sh.OwnerID)
	return err
}

func shareChange(sh domain.Share, op string) domain.Change {
	return domain.Change{OwnerID: sh.OwnerID, Entity: domain.EntityShare, EntityID: sh.ID, Op: op}
}

// ShareStamp is the newest change that could alter what a share renders, so the
// OG image URL changes exactly when the picture would.
const shareStampSQL = `SELECT coalesce(max(c.seq), 0) FROM changes c
	WHERE c.owner_id = $1
	  AND (c.entity IN ('calendar', 'list', 'settings', 'share')
	       OR (c.entity = 'event' AND c.calendar_id = ANY($2))
	       OR (c.entity = 'todo' AND $3))`

func (s *Store) ShareStamp(ctx context.Context, owner domain.ID, cals []domain.ID, todos bool) (int64, error) {
	if cals == nil {
		cals = []domain.ID{}
	}
	var seq int64
	err := s.pool.QueryRow(ctx, shareStampSQL, owner, cals, todos).Scan(&seq)
	return seq, mapErr(err)
}
