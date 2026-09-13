package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/Alexander-D-Karpov/calendar/internal/config"
	"github.com/Alexander-D-Karpov/calendar/internal/term"
)

type featureReport struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Detail  string `json:"detail,omitempty"`
}

type checkReport struct {
	Valid    bool            `json:"valid"`
	Env      string          `json:"env"`
	BaseURL  string          `json:"base_url"`
	Features []featureReport `json:"features"`
	Warnings []string        `json:"warnings"`
}

func configCommand() *command {
	return &command{
		name:    "config",
		summary: "Inspect configuration",
		sub: []*command{
			{name: "print", summary: "Show the effective configuration and where each value comes from", setup: configPrint},
			{name: "check", summary: "Validate configuration and summarize enabled features", setup: configCheck},
		},
	}
}

func configPrint(fs *flag.FlagSet) runFunc {
	changed := fs.Bool("changed", false, "only show values that are explicitly set")
	return func(_ context.Context, a *app, args []string) error {
		if len(args) != 0 {
			return errUsage
		}
		cfg, err := a.config()
		if err != nil {
			return err
		}
		entries := cfg.Entries()
		if *changed {
			entries = slices.DeleteFunc(entries, func(e config.Entry) bool { return e.Default })
		}
		if a.json {
			return a.printJSON(entries)
		}
		p := a.out
		t := p.Table("KEY", "SOURCE", "VALUE")
		for _, e := range entries {
			source := p.Paint(term.Green, "set")
			if e.Default {
				source = p.Paint(term.Dim, "default")
			}
			value := e.Value
			if value == "" {
				value = p.Paint(term.Dim, "-")
			}
			t.Row(e.Key, source, value)
		}
		t.Render()
		return nil
	}
}

func configCheck(*flag.FlagSet) runFunc {
	return func(_ context.Context, a *app, args []string) error {
		if len(args) != 0 {
			return errUsage
		}
		cfg, err := a.config()
		if err != nil {
			return err
		}
		report := checkReport{
			Valid:    true,
			Env:      cfg.App.Env,
			BaseURL:  cfg.App.Origin(),
			Features: features(cfg),
			Warnings: cfg.Warnings,
		}
		if report.Warnings == nil {
			report.Warnings = []string{}
		}
		if a.json {
			return a.printJSON(report)
		}
		for _, w := range cfg.Warnings {
			a.errp.Warn("%s", w)
		}
		p := a.out
		p.OK("configuration is valid")
		fields := []term.Field{
			{Key: "env", Value: report.Env},
			{Key: "base url", Value: report.BaseURL},
		}
		for _, f := range report.Features {
			value := p.Paint(term.Dim, "disabled")
			if f.Enabled {
				value = p.Paint(term.Green, "enabled")
				if f.Detail != "" {
					value += "  " + p.Paint(term.Dim, f.Detail)
				}
			}
			fields = append(fields, term.Field{Key: f.Name, Value: value})
		}
		p.Fields(6, fields...)
		return nil
	}
}

func features(cfg *config.Config) []featureReport {
	registration := featureReport{Name: "registration", Enabled: cfg.Registration.Enabled}
	if registration.Enabled {
		var methods []string
		if cfg.Registration.PasswordEnabled {
			methods = append(methods, "password")
		}
		if cfg.Registration.GoogleEnabled && cfg.Google.Enabled() {
			methods = append(methods, "google")
		}
		registration.Detail = strings.Join(methods, ", ")
		if len(cfg.Registration.AllowedDomains) > 0 {
			registration.Detail += " for " + strings.Join(cfg.Registration.AllowedDomains, ", ")
		}
	}

	google := featureReport{Name: "google", Enabled: cfg.Google.Enabled()}
	if google.Enabled {
		google.Detail = "login"
		if cfg.Google.SyncEnabled {
			mode := "polling"
			if cfg.Google.WebhooksEnabled {
				mode = "webhooks + polling"
			}
			google.Detail += ", sync via " + mode
		}
	}

	smtp := featureReport{Name: "smtp", Enabled: cfg.SMTP.Enabled()}
	if smtp.Enabled {
		smtp.Detail = net.JoinHostPort(cfg.SMTP.Host, strconv.Itoa(cfg.SMTP.Port)) + " " + cfg.SMTP.TLS
	}

	fetch := featureReport{Name: "url fetch", Enabled: true, Detail: "public addresses only"}
	if cfg.Fetch.AllowPrivate {
		fetch.Detail = "private addresses allowed"
	}

	outbound := featureReport{Name: "outbound proxy", Enabled: cfg.Outbound.Enabled()}
	if outbound.Enabled {
		p := cfg.Outbound.Proxy
		outbound.Detail = p.Scheme + "://" + p.Host + " for " + strings.Join(cfg.Outbound.ProxyFor, ", ")
	}

	worker := featureReport{Name: "worker", Enabled: cfg.Worker.Enabled}
	if worker.Enabled {
		worker.Detail = fmt.Sprintf("concurrency %d", cfg.Worker.Concurrency)
	}

	sentry := featureReport{Name: "sentry", Enabled: cfg.Sentry.Enabled()}
	if sentry.Enabled {
		rate := strconv.FormatFloat(cfg.Sentry.TracesSampleRate*100, 'f', -1, 64)
		sentry.Detail = cfg.Sentry.Environment + ", traces " + rate + "%"
	}

	return []featureReport{
		registration,
		google,
		smtp,
		{Name: "web push", Enabled: cfg.WebPush.Enabled()},
		fetch,
		outbound,
		worker,
		{Name: "metrics", Enabled: cfg.Metrics.Addr != "", Detail: cfg.Metrics.Addr},
		sentry,
	}
}
