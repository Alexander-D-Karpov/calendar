//go:build integration

package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Alexander-D-Karpov/calendar/internal/db"
	"github.com/Alexander-D-Karpov/calendar/internal/testutil/pgtest"
)

func TestMigrationsUpDownUp(t *testing.T) {
	tdb := pgtest.New(t)
	ctx := context.Background()

	latest, err := db.LatestMigration()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CheckSchema(ctx, tdb.Pool); err != nil {
		t.Fatalf("after up: %v", err)
	}

	if err := db.MigrateDown(ctx, tdb.URL, 0); err != nil {
		t.Fatalf("down: %v", err)
	}
	var tables int
	err = tdb.Pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_schema = $1 AND table_name <> 'schema_migrations'`,
		tdb.Schema,
	).Scan(&tables)
	if err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatalf("%d tables left after full down migration", tables)
	}

	if err := db.MigrateUp(ctx, tdb.URL, 0); err != nil {
		t.Fatalf("second up: %v", err)
	}
	version, dirty, err := db.SchemaVersion(ctx, tdb.Pool)
	if err != nil {
		t.Fatal(err)
	}
	if version != latest || dirty {
		t.Fatalf("version = %d dirty = %v, want %d clean", version, dirty, latest)
	}
}

func TestMigrateStepsGotoAndShortLimit(t *testing.T) {
	tdb := pgtest.New(t)
	ctx := context.Background()

	latest, err := db.LatestMigration()
	if err != nil {
		t.Fatal(err)
	}
	expect := func(want uint) {
		t.Helper()
		v, dirty, err := db.SchemaVersion(ctx, tdb.Pool)
		if err != nil {
			t.Fatal(err)
		}
		if v != want || dirty {
			t.Fatalf("version = %d dirty = %v, want %d clean", v, dirty, want)
		}
	}

	if err := db.MigrateGoto(ctx, tdb.URL, 5); err != nil {
		t.Fatalf("goto 5: %v", err)
	}
	expect(5)

	if err := db.MigrateUp(ctx, tdb.URL, 2); err != nil {
		t.Fatalf("up 2: %v", err)
	}
	expect(7)

	if err := db.MigrateDown(ctx, tdb.URL, int(latest)+5); err != nil {
		t.Fatalf("down past zero: %v", err)
	}
	expect(0)

	if err := db.MigrateUp(ctx, tdb.URL, int(latest)+5); err != nil {
		t.Fatalf("up past latest: %v", err)
	}
	expect(latest)

	if err := db.MigrateGoto(ctx, tdb.URL, 0); err != nil {
		t.Fatalf("goto 0: %v", err)
	}
	expect(0)

	if err := db.MigrateUp(ctx, tdb.URL, 0); err != nil {
		t.Fatalf("up: %v", err)
	}
	expect(latest)
}

func TestMigrateCanceledContext(t *testing.T) {
	tdb := pgtest.New(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := db.MigrateDown(ctx, tdb.URL, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if err := db.CheckSchema(context.Background(), tdb.Pool); err != nil {
		t.Fatalf("schema changed despite canceled context: %v", err)
	}
}
