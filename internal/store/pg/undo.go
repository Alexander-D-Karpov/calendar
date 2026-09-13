package pg

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const undoColumns = `id, owner_id, kind, label, payload, created_at, used_at`

const (
	insertUndoSQL = `INSERT INTO undo_entries (id, owner_id, kind, label, payload, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`
	claimUndoSQL = `UPDATE undo_entries SET used_at = $3
		WHERE id = $1 AND owner_id = $2 AND used_at IS NULL AND expires_at > $3
		RETURNING ` + undoColumns
)

func scanUndo(row pgx.Row) (domain.UndoEntry, error) {
	var e domain.UndoEntry
	err := row.Scan(&e.ID, &e.OwnerID, &e.Kind, &e.Label, &e.Payload, &e.CreatedAt, &e.UsedAt)
	return e, err
}

func (s *Store) CreateUndo(ctx context.Context, e domain.UndoEntry, expires time.Time) error {
	_, err := s.pool.Exec(ctx, insertUndoSQL, e.ID, e.OwnerID, e.Kind, e.Label, []byte(e.Payload), expires)
	return mapErr(err)
}

func (s *Store) ClaimUndo(ctx context.Context, owner, id domain.ID, at time.Time) (domain.UndoEntry, error) {
	e, err := scanUndo(s.pool.QueryRow(ctx, claimUndoSQL, id, owner, at))
	return e, mapErr(err)
}
