package main

import (
	"context"
	"flag"

	"github.com/Alexander-D-Karpov/calendar/internal/buildinfo"
	"github.com/Alexander-D-Karpov/calendar/internal/term"
)

func versionCommand() *command {
	return &command{name: "version", summary: "Print build information", setup: versionRun}
}

func versionRun(*flag.FlagSet) runFunc {
	return func(_ context.Context, a *app, args []string) error {
		if len(args) != 0 {
			return errUsage
		}
		info := buildinfo.Get()
		if a.json {
			return a.printJSON(info)
		}
		p := a.out
		unknown := p.Paint(term.Dim, "unknown")

		commit := info.Commit
		if commit == "" {
			commit = unknown
		} else if info.Modified {
			commit += p.Paint(term.Yellow, " (modified)")
		}
		built := info.Date
		if built == "" {
			built = unknown
		}

		p.Heading("calendar " + info.Version)
		p.Fields(2,
			term.Field{Key: "commit", Value: commit},
			term.Field{Key: "built", Value: built},
			term.Field{Key: "go", Value: info.GoVersion},
			term.Field{Key: "platform", Value: info.Platform},
		)
		return nil
	}
}
