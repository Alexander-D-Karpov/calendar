package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	codeUniqueViolation      = "23505"
	codeForeignKeyViolation  = "23503"
	codeCheckViolation       = "23514"
	codeSerializationFailure = "40001"
	codeDeadlockDetected     = "40P01"
	codeUndefinedTable       = "42P01"
)

const txAttempts = 3

type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Beginner interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

func InTx(ctx context.Context, b Beginner, fn func(pgx.Tx) error) error {
	return InTxOptions(ctx, b, pgx.TxOptions{}, fn)
}

func InTxOptions(ctx context.Context, b Beginner, opts pgx.TxOptions, fn func(pgx.Tx) error) error {
	for attempt := 1; ; attempt++ {
		err := runTx(ctx, b, opts, fn)
		if err == nil || !IsRetryable(err) || attempt == txAttempts {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(time.Duration(attempt) * 25 * time.Millisecond):
		}
	}
}

func runTx(ctx context.Context, b Beginner, opts pgx.TxOptions, fn func(pgx.Tx) error) error {
	tx, err := b.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(context.WithoutCancel(ctx)); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			return errors.Join(err, rbErr)
		}
		return err
	}
	return tx.Commit(ctx)
}

func pgError(err error) (*pgconn.PgError, bool) {
	var pgErr *pgconn.PgError
	ok := errors.As(err, &pgErr)
	return pgErr, ok
}

func IsNotFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

func IsUniqueViolation(err error, constraint string) bool {
	e, ok := pgError(err)
	return ok && e.Code == codeUniqueViolation && (constraint == "" || e.ConstraintName == constraint)
}

func IsForeignKeyViolation(err error) bool {
	e, ok := pgError(err)
	return ok && e.Code == codeForeignKeyViolation
}

func IsCheckViolation(err error, constraint string) bool {
	e, ok := pgError(err)
	return ok && e.Code == codeCheckViolation && (constraint == "" || e.ConstraintName == constraint)
}

func IsRetryable(err error) bool {
	e, ok := pgError(err)
	return ok && (e.Code == codeSerializationFailure || e.Code == codeDeadlockDetected)
}
