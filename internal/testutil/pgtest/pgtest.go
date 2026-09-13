package pgtest

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
	"github.com/Alexander-D-Karpov/calendar/internal/db"
)

const (
	envKey          = "TEST_DATABASE_URL"
	extensionLockID = 7261001
)

var extensions = []string{"citext", "pg_trgm", "btree_gist", "unaccent"}

type DB struct {
	Pool   *pgxpool.Pool
	URL    string
	Schema string
}

func New(t testing.TB) *DB {
	t.Helper()
	base, err := databaseURL()
	if err != nil {
		t.Fatalf("pgtest: %v", err)
	}
	if base == "" {
		t.Skip(envKey + " is not set in the environment or .env")
	}
	ctx := context.Background()
	schema := "t_" + hex.EncodeToString(crypto.RandomBytes(8))

	if err := prepare(ctx, base, schema); err != nil {
		t.Fatalf("pgtest: prepare: %v", err)
	}
	t.Cleanup(func() {
		if err := dropSchema(context.Background(), base, schema); err != nil {
			t.Errorf("pgtest: drop schema: %v", err)
		}
	})

	dsn, err := withSearchPath(base, schema)
	if err != nil {
		t.Fatalf("pgtest: %v", err)
	}
	if err := db.MigrateUp(ctx, dsn, 0); err != nil {
		t.Fatalf("pgtest: migrate: %v", err)
	}
	pool, err := db.Open(ctx, dsn, 5)
	if err != nil {
		t.Fatalf("pgtest: open: %v", err)
	}
	t.Cleanup(pool.Close)

	return &DB{Pool: pool, URL: dsn, Schema: schema}
}

func databaseURL() (string, error) {
	if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
		return v, nil
	}
	root, err := moduleRoot()
	if err != nil {
		return "", err
	}
	vars, err := godotenv.Read(filepath.Join(root, ".env"))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("read .env: %w", err)
	}
	return strings.TrimSpace(vars[envKey]), nil
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found above working directory")
		}
		dir = parent
	}
}

func prepare(ctx context.Context, base, schema string) error {
	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", extensionLockID); err != nil {
		return err
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", extensionLockID)
	}()
	for _, ext := range extensions {
		stmt := "CREATE EXTENSION IF NOT EXISTS " + pgx.Identifier{ext}.Sanitize() + " WITH SCHEMA public"
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("create extension %s: %w", ext, err)
		}
	}
	_, err = conn.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
	return err
}

func dropSchema(ctx context.Context, base, schema string) error {
	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
	return err
}

func withSearchPath(base, schema string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" {
		return "", fmt.Errorf("%s must be a postgres:// URL", envKey)
	}
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	return u.String(), nil
}
