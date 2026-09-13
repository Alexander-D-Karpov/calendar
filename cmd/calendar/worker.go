package main

import (
	"context"
	"flag"

	calapp "github.com/Alexander-D-Karpov/calendar/internal/app"
)

func workerCommand() *command {
	return &command{name: "worker", summary: "Run background jobs without the web server", setup: workerRun}
}

func workerRun(*flag.FlagSet) runFunc {
	return func(ctx context.Context, a *app, args []string) error {
		if len(args) != 0 {
			return errUsage
		}
		cfg, err := a.config()
		if err != nil {
			return err
		}
		srv, err := calapp.New(ctx, cfg, calapp.Options{Color: a.colorMode, Stderr: a.stderr})
		if err != nil {
			return err
		}
		defer srv.Close()
		return srv.RunWorker(ctx)
	}
}
