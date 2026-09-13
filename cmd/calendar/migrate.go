package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/db"
	"github.com/Alexander-D-Karpov/calendar/internal/term"
)

type migrationReport struct {
	Database string `json:"database"`
	Version  uint   `json:"version"`
	Latest   uint   `json:"latest"`
	Dirty    bool   `json:"dirty"`
	Pending  uint   `json:"pending"`
	Previous *uint  `json:"previous,omitempty"`
	TookMS   *int64 `json:"took_ms,omitempty"`
}

func migrateCommand() *command {
	return &command{
		name:    "migrate",
		summary: "Manage the database schema",
		sub: []*command{
			{name: "status", summary: "Show the current schema version", setup: migrateStatus},
			{name: "up", summary: "Apply pending migrations, all or the next n", args: "[n]", setup: migrateUp},
			{name: "down", summary: "Roll back migrations", args: "<n|all>", setup: migrateDown},
			{name: "goto", summary: "Migrate up or down to a specific version", args: "<version>", setup: migrateGoto},
			{name: "force", summary: "Set the schema version without running migrations", args: "<version|none>", setup: migrateForce},
		},
	}
}

func (a *app) databaseURL() (string, error) {
	cfg, err := a.config()
	if err != nil {
		return "", err
	}
	return cfg.Database.URL, nil
}

func addYesFlag(fs *flag.FlagSet) *bool {
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	fs.BoolVar(yes, "y", false, "shorthand for --yes")
	return yes
}

func migrateStatus(*flag.FlagSet) runFunc {
	return func(ctx context.Context, a *app, args []string) error {
		if len(args) != 0 {
			return errUsage
		}
		dsn, err := a.databaseURL()
		if err != nil {
			return err
		}
		st, err := db.MigrationState(ctx, dsn)
		if err != nil {
			return err
		}
		return a.reportMigration(dsn, st, nil, 0, "")
	}
}

func migrateUp(*flag.FlagSet) runFunc {
	return func(ctx context.Context, a *app, args []string) error {
		steps := 0
		switch len(args) {
		case 0:
		case 1:
			n, ok := parseCount(args[0])
			if !ok {
				return errUsage
			}
			steps = n
		default:
			return errUsage
		}
		dsn, err := a.databaseURL()
		if err != nil {
			return err
		}
		before, err := db.MigrationState(ctx, dsn)
		if err != nil {
			return err
		}
		if err := ensureClean(before); err != nil {
			return err
		}
		return a.runMigration(ctx, dsn, before, func() error {
			return db.MigrateUp(ctx, dsn, steps)
		})
	}
}

func migrateDown(fs *flag.FlagSet) runFunc {
	yes := addYesFlag(fs)
	return func(ctx context.Context, a *app, args []string) error {
		if len(args) != 1 {
			return errUsage
		}
		steps := 0
		label := "all migrations"
		if args[0] != "all" {
			n, ok := parseCount(args[0])
			if !ok {
				return errUsage
			}
			steps = n
			label = plural(n, "migration")
		}
		dsn, err := a.databaseURL()
		if err != nil {
			return err
		}
		before, err := db.MigrationState(ctx, dsn)
		if err != nil {
			return err
		}
		if err := ensureClean(before); err != nil {
			return err
		}
		if before.Version == 0 {
			return a.reportMigration(dsn, before, nil, 0, "")
		}
		if err := a.confirm(ctx, fmt.Sprintf("Roll back %s on %s?", label, dbLabel(dsn)), *yes); err != nil {
			return err
		}
		return a.runMigration(ctx, dsn, before, func() error {
			return db.MigrateDown(ctx, dsn, steps)
		})
	}
}

func migrateGoto(fs *flag.FlagSet) runFunc {
	yes := addYesFlag(fs)
	return func(ctx context.Context, a *app, args []string) error {
		if len(args) != 1 {
			return errUsage
		}
		target, err := strconv.ParseUint(args[0], 10, 32)
		if err != nil {
			return errUsage
		}
		version := uint(target)
		dsn, err := a.databaseURL()
		if err != nil {
			return err
		}
		before, err := db.MigrationState(ctx, dsn)
		if err != nil {
			return err
		}
		if err := ensureClean(before); err != nil {
			return err
		}
		if version > before.Latest {
			return fmt.Errorf("version %d does not exist, latest is %d", version, before.Latest)
		}
		if version == before.Version {
			return a.reportMigration(dsn, before, nil, 0, "")
		}
		if version < before.Version {
			question := fmt.Sprintf("Roll back %s from version %d to %d?", dbLabel(dsn), before.Version, version)
			if err := a.confirm(ctx, question, *yes); err != nil {
				return err
			}
		}
		return a.runMigration(ctx, dsn, before, func() error {
			return db.MigrateGoto(ctx, dsn, version)
		})
	}
}

func migrateForce(fs *flag.FlagSet) runFunc {
	yes := addYesFlag(fs)
	return func(ctx context.Context, a *app, args []string) error {
		if len(args) != 1 {
			return errUsage
		}
		version, err := parseForceVersion(args[0])
		if err != nil {
			return errUsage
		}
		latest, err := db.LatestMigration()
		if err != nil {
			return err
		}
		if version > int(latest) {
			return fmt.Errorf("version %d does not exist, latest is %d", version, latest)
		}
		dsn, err := a.databaseURL()
		if err != nil {
			return err
		}
		label := strconv.Itoa(version)
		if version == -1 {
			label = "none"
		}
		question := fmt.Sprintf("Set schema version of %s to %s without running migrations?", dbLabel(dsn), label)
		if err := a.confirm(ctx, question, *yes); err != nil {
			return err
		}
		if err := db.MigrateForce(ctx, dsn, version); err != nil {
			return err
		}
		st, err := db.MigrationState(ctx, dsn)
		if err != nil {
			return err
		}
		headline := fmt.Sprintf("schema version set to %d", st.Version)
		if version == -1 {
			headline = "schema version cleared"
		}
		return a.reportMigration(dsn, st, nil, 0, headline)
	}
}

func (a *app) runMigration(ctx context.Context, dsn string, before db.MigrationStatus, fn func() error) error {
	start := time.Now()
	if err := fn(); err != nil {
		return err
	}
	took := time.Since(start)
	after, err := db.MigrationState(ctx, dsn)
	if err != nil {
		return err
	}
	return a.reportMigration(dsn, after, &before.Version, took, "")
}

func (a *app) reportMigration(dsn string, st db.MigrationStatus, previous *uint, took time.Duration, headline string) error {
	r := migrationReport{
		Database: dbLabel(dsn),
		Version:  st.Version,
		Latest:   st.Latest,
		Dirty:    st.Dirty,
		Previous: previous,
	}
	if st.Latest > st.Version {
		r.Pending = st.Latest - st.Version
	}
	if previous != nil {
		ms := took.Milliseconds()
		r.TookMS = &ms
	}
	if a.json {
		return a.printJSON(r)
	}

	p := a.out
	switch {
	case headline != "":
		p.OK("%s", headline)
	case st.Dirty:
		p.Warn("schema version %d is dirty", st.Version)
	case previous != nil && *previous != st.Version:
		p.OK("migrated from version %d to %d in %s", *previous, st.Version, took.Round(time.Millisecond))
	case st.Version == st.Latest:
		p.OK("schema is up to date")
	case st.Version < st.Latest:
		p.Warn("%s pending", plural(int(r.Pending), "migration"))
	default:
		p.Warn("schema version %d is newer than this binary supports", st.Version)
	}
	fields := []term.Field{
		{Key: "database", Value: r.Database},
		{Key: "version", Value: fmt.Sprintf("%d of %d", st.Version, st.Latest)},
	}
	if st.Dirty {
		fields = append(fields, term.Field{Key: "fix", Value: p.Paint(term.Dim, "calendar migrate force <last good version>")})
	}
	p.Fields(6, fields...)
	return nil
}

func ensureClean(st db.MigrationStatus) error {
	if st.Dirty {
		return fmt.Errorf("schema version %d is dirty, fix it and run: calendar migrate force <version>", st.Version)
	}
	return nil
}

func parseCount(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil && n > 0
}

func parseForceVersion(s string) (int, error) {
	if s == "none" {
		return -1, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < -1 {
		return 0, fmt.Errorf("invalid version %q", s)
	}
	return n, nil
}

func dbLabel(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.Host == "" {
		return "database"
	}
	return u.Host + u.Path
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
