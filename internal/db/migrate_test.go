package db

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/Alexander-D-Karpov/calendar/migrations"
)

func TestMigrationFilesPaired(t *testing.T) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	ups := map[string]bool{}
	downs := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasSuffix(name, ".up.sql"):
			ups[strings.TrimSuffix(name, ".up.sql")] = true
		case strings.HasSuffix(name, ".down.sql"):
			downs[strings.TrimSuffix(name, ".down.sql")] = true
		case strings.HasSuffix(name, ".sql"):
			t.Errorf("migration %s must end with .up.sql or .down.sql", name)
		}
	}
	for base := range ups {
		if !downs[base] {
			t.Errorf("missing down migration for %s", base)
		}
	}
	for base := range downs {
		if !ups[base] {
			t.Errorf("missing up migration for %s", base)
		}
	}
}

func TestMigrationVersionsSequential(t *testing.T) {
	versions, err := migrationVersions()
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) == 0 {
		t.Fatal("no migrations")
	}
	for i, v := range versions {
		if v != uint(i+1) {
			t.Fatalf("migration versions must be 1..n without gaps, got %v", versions)
		}
	}
	latest, err := LatestMigration()
	if err != nil {
		t.Fatal(err)
	}
	if latest != versions[len(versions)-1] {
		t.Fatalf("LatestMigration() = %d", latest)
	}
}

func TestMigrateURL(t *testing.T) {
	got, err := migrateURL("postgres://u:p@localhost:5432/db?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "pgx5://u:p@localhost:5432/db") {
		t.Fatalf("migrateURL = %q", got)
	}
	if _, err := migrateURL("mysql://localhost/db"); err == nil {
		t.Fatal("expected error for mysql scheme")
	}
}
