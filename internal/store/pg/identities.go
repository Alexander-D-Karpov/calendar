package pg

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/Alexander-D-Karpov/calendar/internal/db"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const identityColumns = `id, user_id, provider, subject, email, linked_at`

const (
	identityBySubjectSQL = `SELECT ` + identityColumns + ` FROM user_identities WHERE provider = $1 AND subject = $2`
	listIdentitiesSQL    = `SELECT ` + identityColumns + ` FROM user_identities WHERE user_id = $1 ORDER BY provider`
	insertIdentitySQL    = `INSERT INTO user_identities (id, user_id, provider, subject, email, linked_at) VALUES ($1, $2, $3, $4, $5, $6)`
	deleteIdentitySQL    = `DELETE FROM user_identities WHERE user_id = $1 AND provider = $2`
)

func scanIdentity(row pgx.Row) (domain.Identity, error) {
	var i domain.Identity
	err := row.Scan(&i.ID, &i.UserID, &i.Provider, &i.Subject, &i.Email, &i.LinkedAt)
	return i, err
}

func (s *Store) IdentityBySubject(ctx context.Context, provider, subject string) (domain.Identity, error) {
	i, err := scanIdentity(s.pool.QueryRow(ctx, identityBySubjectSQL, provider, subject))
	return i, mapErr(err)
}

func (s *Store) ListIdentities(ctx context.Context, userID domain.ID) ([]domain.Identity, error) {
	rows, err := s.pool.Query(ctx, listIdentitiesSQL, userID)
	if err != nil {
		return nil, mapErr(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (domain.Identity, error) { return scanIdentity(r) })
	return out, mapErr(err)
}

func (s *Store) CreateIdentity(ctx context.Context, i domain.Identity) error {
	return mapErr(insertIdentity(ctx, s.pool, i))
}

func (s *Store) DeleteIdentity(ctx context.Context, userID domain.ID, provider string) error {
	tag, err := s.pool.Exec(ctx, deleteIdentitySQL, userID, provider)
	return affected(tag.RowsAffected(), err)
}

func insertIdentity(ctx context.Context, q db.DBTX, i domain.Identity) error {
	_, err := q.Exec(ctx, insertIdentitySQL, i.ID, i.UserID, i.Provider, i.Subject, i.Email, i.LinkedAt)
	return err
}
