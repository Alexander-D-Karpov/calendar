package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	xterm "golang.org/x/term"

	calapp "github.com/Alexander-D-Karpov/calendar/internal/app"
	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/db"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/store/pg"
	"github.com/Alexander-D-Karpov/calendar/internal/term"
)

var cliMeta = auth.Meta{UserAgent: "calendar-cli"}

type userReport struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Timezone    string    `json:"timezone"`
	Verified    bool      `json:"verified"`
	Disabled    bool      `json:"disabled"`
	HasPassword bool      `json:"has_password"`
	CreatedAt   time.Time `json:"created_at"`
}

func toUserReport(u domain.User) userReport {
	return userReport{
		ID:          u.ID.String(),
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Timezone:    u.Timezone,
		Verified:    u.Verified(),
		Disabled:    u.Disabled(),
		HasPassword: u.HasPassword(),
		CreatedAt:   u.CreatedAt,
	}
}

func userCommand() *command {
	return &command{
		name:    "user",
		summary: "Manage user accounts",
		sub: []*command{
			{name: "list", summary: "List all users", setup: userList},
			{name: "create", summary: "Create a user", setup: userCreate},
			{name: "set-password", summary: "Set a new password and sign out all sessions", args: "<email>", setup: userSetPassword},
			{name: "disable", summary: "Disable a user and sign out all sessions", args: "<email>", setup: userDisable},
			{name: "enable", summary: "Re-enable a disabled user", args: "<email>", setup: userEnable},
		},
	}
}

func (a *app) authService(ctx context.Context) (*auth.Service, func(), error) {
	cfg, err := a.config()
	if err != nil {
		return nil, nil, err
	}
	pool, err := db.Open(ctx, cfg.Database.URL, 2)
	if err != nil {
		return nil, nil, err
	}
	if err := db.CheckSchema(ctx, pool); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("%w, run: calendar migrate up", err)
	}
	svc, err := calapp.NewAuth(cfg, pg.New(pool), clock.New(), slog.New(slog.DiscardHandler))
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	return svc, pool.Close, nil
}

func (a *app) withUser(ctx context.Context, email string, fn func(*auth.Service, domain.User) error) error {
	svc, done, err := a.authService(ctx)
	if err != nil {
		return err
	}
	defer done()
	u, err := svc.UserByEmail(ctx, email)
	if errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("user %s not found", email)
	}
	if err != nil {
		return err
	}
	return fn(svc, u)
}

func userList(*flag.FlagSet) runFunc {
	return func(ctx context.Context, a *app, args []string) error {
		if len(args) != 0 {
			return errUsage
		}
		svc, done, err := a.authService(ctx)
		if err != nil {
			return err
		}
		defer done()
		users, err := svc.ListUsers(ctx)
		if err != nil {
			return err
		}
		if a.json {
			out := make([]userReport, len(users))
			for i, u := range users {
				out[i] = toUserReport(u)
			}
			return a.printJSON(out)
		}
		p := a.out
		if len(users) == 0 {
			p.Hint("no users yet, create one with: calendar user create --email <address>")
			return nil
		}
		t := p.Table("EMAIL", "NAME", "STATUS", "LOGIN", "CREATED")
		for _, u := range users {
			t.Row(u.Email, u.DisplayName, userStatus(p, u), loginMethod(p, u), u.CreatedAt.Local().Format("2006-01-02 15:04"))
		}
		t.Render()
		return nil
	}
}

func userCreate(fs *flag.FlagSet) runFunc {
	email := fs.String("email", "", "email `address` of the new user")
	name := fs.String("name", "", "display `name`")
	tz := fs.String("timezone", "", "IANA `zone`, defaults to DEFAULT_TIMEZONE")
	unverified := fs.Bool("unverified", false, "leave the email address unverified")
	stdin := fs.Bool("password-stdin", false, "read the password from stdin")
	return func(ctx context.Context, a *app, args []string) error {
		if len(args) != 0 || strings.TrimSpace(*email) == "" {
			return errUsage
		}
		pw, err := a.readPassword(ctx, *stdin)
		if err != nil {
			return err
		}
		svc, done, err := a.authService(ctx)
		if err != nil {
			return err
		}
		defer done()
		u, err := svc.CreateUser(ctx, auth.CreateUserInput{
			RegisterInput: auth.RegisterInput{Email: *email, Password: pw, DisplayName: *name, Timezone: *tz},
			Verified:      !*unverified,
		}, cliMeta)
		if err != nil {
			return err
		}
		if a.json {
			return a.printJSON(toUserReport(u))
		}
		a.out.OK("created %s", u.Email)
		a.out.Fields(6,
			term.Field{Key: "id", Value: u.ID.String()},
			term.Field{Key: "verified", Value: yesNo(u.Verified())},
			term.Field{Key: "timezone", Value: u.Timezone},
		)
		return nil
	}
}

func userSetPassword(fs *flag.FlagSet) runFunc {
	stdin := fs.Bool("password-stdin", false, "read the password from stdin")
	return func(ctx context.Context, a *app, args []string) error {
		if len(args) != 1 {
			return errUsage
		}
		pw, err := a.readPassword(ctx, *stdin)
		if err != nil {
			return err
		}
		return a.withUser(ctx, args[0], func(svc *auth.Service, u domain.User) error {
			if err := svc.SetPassword(ctx, u.ID, pw, cliMeta); err != nil {
				return err
			}
			a.out.OK("password updated for %s, all sessions signed out", u.Email)
			return nil
		})
	}
}

func userDisable(fs *flag.FlagSet) runFunc {
	return userToggle(addYesFlag(fs), true)
}

func userEnable(*flag.FlagSet) runFunc {
	yes := true
	return userToggle(&yes, false)
}

func userToggle(yes *bool, disable bool) runFunc {
	return func(ctx context.Context, a *app, args []string) error {
		if len(args) != 1 {
			return errUsage
		}
		return a.withUser(ctx, args[0], func(svc *auth.Service, u domain.User) error {
			if disable {
				if err := a.confirm(ctx, fmt.Sprintf("Disable %s and sign out all sessions?", u.Email), *yes); err != nil {
					return err
				}
			}
			if err := svc.SetDisabled(ctx, u.ID, disable, cliMeta); err != nil {
				return err
			}
			if disable {
				a.out.OK("disabled %s", u.Email)
			} else {
				a.out.OK("enabled %s", u.Email)
			}
			return nil
		})
	}
}

func (a *app) readPassword(ctx context.Context, fromStdin bool) (string, error) {
	if fromStdin {
		b, err := io.ReadAll(io.LimitReader(a.in, auth.MaxPasswordBytes+2))
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	pw, err := a.readSecret(ctx, "New password:")
	if err != nil {
		return "", err
	}
	again, err := a.readSecret(ctx, "Repeat password:")
	if err != nil {
		return "", err
	}
	if pw != again {
		return "", errors.New("passwords do not match")
	}
	return pw, nil
}

func (a *app) readSecret(ctx context.Context, prompt string) (string, error) {
	f, ok := a.in.(interface{ Fd() uintptr })
	if !ok || !term.IsTerminal(a.in) {
		return "", errors.New("stdin is not a terminal, use --password-stdin")
	}
	fd := int(f.Fd())
	state, err := xterm.GetState(fd)
	if err != nil {
		return "", err
	}
	a.errp.Printf("%s ", a.errp.Paint(term.Bold, prompt))
	type result struct {
		b   []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		b, err := xterm.ReadPassword(fd)
		ch <- result{b: b, err: err}
	}()
	select {
	case <-ctx.Done():
		_ = xterm.Restore(fd, state)
		a.errp.Newline()
		return "", ctx.Err()
	case r := <-ch:
		a.errp.Newline()
		return string(r.b), r.err
	}
}

func userStatus(p *term.Printer, u domain.User) string {
	switch {
	case u.Disabled():
		return p.Paint(term.Red, "disabled")
	case !u.Verified():
		return p.Paint(term.Yellow, "unverified")
	}
	return p.Paint(term.Green, "active")
}

func loginMethod(p *term.Printer, u domain.User) string {
	if u.HasPassword() {
		return "password"
	}
	return p.Paint(term.Dim, "external")
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
