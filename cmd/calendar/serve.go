package main

import (
	"context"
	"flag"

	calapp "github.com/Alexander-D-Karpov/calendar/internal/app"
	"github.com/Alexander-D-Karpov/calendar/internal/buildinfo"
)

func serveCommand() *command {
	return &command{name: "serve", summary: "Run the web server", setup: serveRun}
}

func serveRun(fs *flag.FlagSet) runFunc {
	noMigrate := fs.Bool("no-migrate", false, "skip migrations even when DATABASE_AUTO_MIGRATE is true")
	return func(ctx context.Context, a *app, args []string) error {
		if len(args) != 0 {
			return errUsage
		}
		cfg, err := a.config()
		if err != nil {
			return err
		}
		srv, err := calapp.New(ctx, cfg, calapp.Options{
			Color:   a.colorMode,
			Stderr:  a.stderr,
			Migrate: cfg.Database.AutoMigrate && !*noMigrate,
		})
		if err != nil {
			return err
		}
		defer srv.Close()

		for _, w := range cfg.Warnings {
			srv.Logger.Warn(w)
		}
		info := buildinfo.Get()
		srv.Logger.Info("starting",
			"version", info.Version,
			"commit", info.ShortCommit(),
			"env", cfg.App.Env,
			"base_url", cfg.App.Origin(),
		)
		handler, err := srv.Handler()
		if err != nil {
			return err
		}
		return srv.Serve(ctx, handler)
	}
}
