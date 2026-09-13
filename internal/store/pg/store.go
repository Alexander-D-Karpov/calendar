package pg

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Alexander-D-Karpov/calendar/internal/db"
	"github.com/Alexander-D-Karpov/calendar/internal/db/sqlc"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

type Store struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: sqlc.New(pool)}
}

func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

func (s *Store) tx(ctx context.Context, fn func(*sqlc.Queries) error) error {
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		return fn(s.q.WithTx(tx))
	})
}

func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case db.IsNotFound(err):
		return domain.ErrNotFound
	case db.IsUniqueViolation(err, ""):
		return fmt.Errorf("%w: %w", domain.ErrConflict, err)
	case db.IsForeignKeyViolation(err):
		return fmt.Errorf("%w: %w", domain.ErrNotFound, err)
	case db.IsCheckViolation(err, ""):
		return fmt.Errorf("%w: %w", domain.ErrInvalid, err)
	}
	return err
}

func affected(n int64, err error) error {
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func textPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func text(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func addrPtr(a netip.Addr) *netip.Addr {
	if !a.IsValid() {
		return nil
	}
	a = a.Unmap()
	return &a
}

func addr(p *netip.Addr) netip.Addr {
	if p == nil {
		return netip.Addr{}
	}
	return p.Unmap()
}

func int32s(in []int) []int32 {
	out := make([]int32, len(in))
	for i, v := range in {
		out[i] = int32(v)
	}
	return out
}

func ints(in []int32) []int {
	out := make([]int, len(in))
	for i, v := range in {
		out[i] = int(v)
	}
	return out
}

func (s *Store) rawTx(ctx context.Context, fn func(pgx.Tx, *sqlc.Queries) error) error {
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		return fn(tx, s.q.WithTx(tx))
	})
}

func pgDate(t time.Time) pgtype.Date {
	return pgtype.Date{Time: time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), Valid: true}
}

func clockMinutes(t pgtype.Time) int {
	if !t.Valid {
		return 0
	}
	return int(t.Microseconds / 60_000_000)
}
