package main

import (
	"context"
	"flag"

	"github.com/Alexander-D-Karpov/calendar/internal/db"
	"github.com/Alexander-D-Karpov/calendar/internal/gsync"
	"github.com/Alexander-D-Karpov/calendar/internal/jobs"
	"github.com/Alexander-D-Karpov/calendar/internal/store/pg"
)

func syncCommand() *command {
	return &command{
		name:    "sync",
		summary: "Google sync maintenance",
		sub: []*command{
			{name: "run", summary: "Queue a pull for every enabled binding of a user", args: "<email>", setup: syncRun},
		},
	}
}

// syncRun queues the same job the Sync now button does. Engine.Run only
// enqueues, so this enqueues too rather than building the whole engine with
// its OAuth and network stack just to write a row the worker picks up.
func syncRun(*flag.FlagSet) runFunc {
	return func(ctx context.Context, a *app, args []string) error {
		if len(args) != 1 {
			return errUsage
		}
		owner, err := a.ownerByEmail(ctx, args[0])
		if err != nil {
			return err
		}
		cfg, err := a.config()
		if err != nil {
			return err
		}
		pool, err := db.Open(ctx, cfg.Database.URL, 2)
		if err != nil {
			return err
		}
		defer pool.Close()

		bindings, err := pg.New(pool).Bindings(ctx, owner)
		if err != nil {
			return err
		}
		queued := 0
		for _, b := range bindings {
			if !b.Enabled {
				continue
			}
			if err := jobs.Enqueue(ctx, pool, jobs.KindGooglePull,
				gsync.BindingJob{Binding: b.ID},
				jobs.Options{UniqueKey: b.ID.String()}); err != nil {
				return err
			}
			queued++
		}
		if queued == 0 {
			a.out.Printf("no enabled sync bindings for %s\n", args[0])
			return nil
		}
		a.out.Printf("queued a pull for %d binding(s); the worker runs them\n", queued)
		return nil
	}
}
