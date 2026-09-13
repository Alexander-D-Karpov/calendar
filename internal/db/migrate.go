package db

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/Alexander-D-Karpov/calendar/migrations"
)

var ErrMigrationStopped = errors.New("migration stopped before completion")

type MigrationStatus struct {
	Version uint
	Dirty   bool
	Latest  uint
}

func MigrateUp(ctx context.Context, databaseURL string, steps int) error {
	return withMigrator(ctx, databaseURL, true, func(m *migrate.Migrate) error {
		var err error
		if steps <= 0 {
			err = m.Up()
		} else {
			err = m.Steps(steps)
		}
		return migrationResult("migrate up", err)
	})
}

func MigrateDown(ctx context.Context, databaseURL string, steps int) error {
	return withMigrator(ctx, databaseURL, true, func(m *migrate.Migrate) error {
		var err error
		if steps <= 0 {
			err = m.Down()
		} else {
			err = m.Steps(-steps)
		}
		return migrationResult("migrate down", err)
	})
}

func MigrateGoto(ctx context.Context, databaseURL string, version uint) error {
	return withMigrator(ctx, databaseURL, true, func(m *migrate.Migrate) error {
		var err error
		if version == 0 {
			err = m.Down()
		} else {
			err = m.Migrate(version)
		}
		return migrationResult("migrate goto", err)
	})
}

func MigrateForce(ctx context.Context, databaseURL string, version int) error {
	return withMigrator(ctx, databaseURL, false, func(m *migrate.Migrate) error {
		if err := m.Force(version); err != nil {
			return fmt.Errorf("migrate force: %w", err)
		}
		return nil
	})
}

func MigrationState(ctx context.Context, databaseURL string) (MigrationStatus, error) {
	var st MigrationStatus
	latest, err := LatestMigration()
	if err != nil {
		return st, err
	}
	st.Latest = latest
	err = withMigrator(ctx, databaseURL, false, func(m *migrate.Migrate) error {
		v, dirty, err := m.Version()
		if errors.Is(err, migrate.ErrNilVersion) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("migration version: %w", err)
		}
		st.Version, st.Dirty = v, dirty
		return nil
	})
	return st, err
}

func LatestMigration() (uint, error) {
	versions, err := migrationVersions()
	if err != nil {
		return 0, err
	}
	if len(versions) == 0 {
		return 0, errors.New("no migrations found")
	}
	return versions[len(versions)-1], nil
}

func SchemaVersion(ctx context.Context, q DBTX) (uint, bool, error) {
	var version int64
	var dirty bool
	err := q.QueryRow(ctx, "SELECT version, dirty FROM schema_migrations LIMIT 1").Scan(&version, &dirty)
	if IsNotFound(err) {
		return 0, false, nil
	}
	if e, ok := pgError(err); ok && e.Code == codeUndefinedTable {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return uint(version), dirty, nil
}

func CheckSchema(ctx context.Context, q DBTX) error {
	latest, err := LatestMigration()
	if err != nil {
		return err
	}
	version, dirty, err := SchemaVersion(ctx, q)
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("database schema version %d is dirty", version)
	}
	if version != latest {
		return fmt.Errorf("database schema version is %d, expected %d", version, latest)
	}
	return nil
}

func migrationResult(op string, err error) error {
	var short migrate.ErrShortLimit
	switch {
	case err == nil, errors.Is(err, migrate.ErrNoChange), errors.As(err, &short):
		return nil
	}
	return fmt.Errorf("%s: %w", op, err)
}

func withMigrator(ctx context.Context, databaseURL string, strict bool, fn func(*migrate.Migrate) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strict {
		if err := validateMigrations(); err != nil {
			return err
		}
	}
	target, err := migrateURL(databaseURL)
	if err != nil {
		return err
	}
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, target)
	if err != nil {
		return fmt.Errorf("open migrator: %w", err)
	}
	stop := context.AfterFunc(ctx, func() {
		select {
		case m.GracefulStop <- true:
		default:
		}
	})
	runErr := fn(m)
	interrupted := !stop()
	srcErr, dbErr := m.Close()
	if interrupted && runErr == nil {
		runErr = errors.Join(ErrMigrationStopped, ctx.Err())
	}
	return errors.Join(runErr, srcErr, dbErr)
}

func migrateURL(databaseURL string) (string, error) {
	u, err := url.Parse(databaseURL)
	if err != nil {
		return "", errors.New("invalid database url")
	}
	switch u.Scheme {
	case "postgres", "postgresql":
		u.Scheme = "pgx5"
	default:
		return "", fmt.Errorf("unsupported database url scheme %q", u.Scheme)
	}
	return u.String(), nil
}

func migrationVersions() ([]uint, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, err
	}
	var versions []uint
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			return nil, fmt.Errorf("invalid migration name %s", name)
		}
		v, err := strconv.ParseUint(prefix, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid migration name %s", name)
		}
		versions = append(versions, uint(v))
	}
	slices.Sort(versions)
	return versions, nil
}

func validateMigrations() error {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return err
	}
	var empty []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		data, err := fs.ReadFile(migrations.FS, e.Name())
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(data)) == 0 {
			empty = append(empty, e.Name())
		}
	}
	if len(empty) > 0 {
		return fmt.Errorf("refusing to migrate, empty migration files: %s", strings.Join(empty, ", "))
	}
	return nil
}
