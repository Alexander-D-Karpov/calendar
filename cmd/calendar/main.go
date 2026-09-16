package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	_ "time/tzdata"

	"github.com/Alexander-D-Karpov/calendar/internal/config"
	"github.com/Alexander-D-Karpov/calendar/internal/term"
)

var (
	errUsage   = errors.New("usage")
	errAborted = errors.New("aborted")
)

var globalFlags = map[string]bool{
	"env-file": true,
	"color":    true,
	"json":     true,
}

type exitError struct {
	code int
}

func (e exitError) Error() string {
	return fmt.Sprintf("exit status %d", e.code)
}

type runFunc func(ctx context.Context, a *app, args []string) error

type command struct {
	name    string
	summary string
	args    string
	setup   func(fs *flag.FlagSet) runFunc
	sub     []*command
}

func (c *command) find(name string) *command {
	for _, s := range c.sub {
		if s.name == name {
			return s
		}
	}
	return nil
}

type app struct {
	envFile    string
	envFileSet bool
	color      string
	colorMode  term.ColorMode
	json       bool
	in         io.Reader
	stdout     io.Writer
	stderr     io.Writer
	out        *term.Printer
	errp       *term.Printer
	cfg        *config.Config
}

func rootCommand() *command {
	return &command{
		name:    "calendar",
		summary: "Calendar and todo server",
		sub: []*command{
			serveCommand(),
			workerCommand(),
			userCommand(),
			configCommand(),
			migrateCommand(),
			dedupCommand(),
			keysCommand(),
			vapidCommand(),
			healthcheckCommand(),
			versionCommand(),
		},
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	a := &app{
		in:      stdin,
		stdout:  stdout,
		stderr:  stderr,
		envFile: ".env",
		color:   term.ColorAuto.String(),
	}
	a.setPrinters(term.ColorAuto)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := rootCommand()
	return a.exitCode(ctx, a.dispatch(ctx, root, []string{root.name}, args))
}

func (a *app) setPrinters(mode term.ColorMode) {
	a.colorMode = mode
	a.out = term.NewPrinter(a.stdout, mode)
	a.errp = term.NewPrinter(a.stderr, mode)
}

func (a *app) newFlagSet(path []string) *flag.FlagSet {
	fs := flag.NewFlagSet(strings.Join(path, " "), flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&a.envFile, "env-file", a.envFile, "load environment from `path`")
	fs.StringVar(&a.color, "color", a.color, "output coloring `mode`: auto, always or never")
	fs.BoolVar(&a.json, "json", a.json, "print machine readable JSON where supported")
	return fs
}

func (a *app) applyGlobals(fs *flag.FlagSet) error {
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "env-file" {
			a.envFileSet = true
		}
	})
	mode, err := term.ParseColorMode(a.color)
	if err != nil {
		return err
	}
	a.setPrinters(mode)
	return nil
}

func (a *app) dispatch(ctx context.Context, cmd *command, path, args []string) error {
	fs := a.newFlagSet(path)
	var runFn runFunc
	if cmd.setup != nil {
		runFn = cmd.setup(fs)
	}
	rest, err := parseFlags(fs, args, runFn != nil)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			a.printHelp(a.out, cmd, path, fs)
			return nil
		}
		a.errp.Error("%v", err)
		a.errp.Detail("usage: %s", usageLine(cmd, path, fs))
		return exitError{2}
	}
	if err := a.applyGlobals(fs); err != nil {
		a.errp.Error("%v", err)
		return exitError{2}
	}
	if runFn == nil {
		return a.dispatchSub(ctx, cmd, path, fs, rest)
	}
	err = runFn(ctx, a, rest)
	if errors.Is(err, errUsage) {
		a.printHelp(a.errp, cmd, path, fs)
		return exitError{2}
	}
	return err
}

func (a *app) dispatchSub(ctx context.Context, cmd *command, path []string, fs *flag.FlagSet, rest []string) error {
	if len(rest) == 0 {
		a.printHelp(a.errp, cmd, path, fs)
		return exitError{2}
	}
	if isHelp(rest[0]) {
		a.printHelp(a.out, cmd, path, fs)
		return nil
	}
	sub := cmd.find(rest[0])
	if sub == nil {
		a.errp.Error("unknown command %q", rest[0])
		a.printHelp(a.errp, cmd, path, fs)
		return exitError{2}
	}
	return a.dispatch(ctx, sub, append(slices.Clone(path), sub.name), rest[1:])
}

func parseFlags(fs *flag.FlagSet, args []string, interspersed bool) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if !interspersed || len(rest) == 0 {
			return append(positional, rest...), nil
		}
		consumed := len(args) - len(rest)
		if consumed > 0 && args[consumed-1] == "--" {
			return append(positional, rest...), nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

func (a *app) exitCode(ctx context.Context, err error) int {
	var ee exitError
	var cfgErr *config.Error
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee):
		return ee.code
	case errors.Is(err, errAborted):
		a.errp.Warn("aborted")
		return 1
	case ctx.Err() != nil:
		a.errp.Warn("interrupted")
		return 130
	case errors.As(err, &cfgErr):
		a.errp.Error("invalid configuration")
		for _, p := range cfgErr.Problems {
			if key, msg, ok := strings.Cut(p, ": "); ok {
				a.errp.Detail("%s %s", a.errp.Paint(term.Bold, key), msg)
			} else {
				a.errp.Detail("%s", p)
			}
		}
		return 1
	}
	a.errp.Error("%v", err)
	return 1
}

func (a *app) printHelp(p *term.Printer, cmd *command, path []string, fs *flag.FlagSet) {
	if cmd.summary != "" {
		p.Heading(cmd.summary)
		p.Newline()
	}
	p.Heading("Usage")
	p.Printf("  %s\n", usageLine(cmd, path, fs))
	if len(cmd.sub) > 0 {
		p.Newline()
		p.Heading("Commands")
		t := p.Table().Indent(2)
		for _, s := range cmd.sub {
			t.Row(p.Paint(term.Cyan, s.name), s.summary)
		}
		t.Render()
	}
	printFlags(p, "Flags", fs, isLocalFlag)
	if len(path) == 1 {
		printFlags(p, "Global flags", fs, isGlobalFlag)
	}
}

func isGlobalFlag(name string) bool {
	return globalFlags[name]
}

func isLocalFlag(name string) bool {
	return !globalFlags[name]
}

func printFlags(p *term.Printer, title string, fs *flag.FlagSet, include func(string) bool) {
	t := p.Table().Indent(2)
	fs.VisitAll(func(f *flag.Flag) {
		if !include(f.Name) {
			return
		}
		placeholder, usage := flag.UnquoteUsage(f)
		name := "--" + f.Name
		if len(f.Name) == 1 {
			name = "-" + f.Name
		}
		if placeholder != "" {
			name += " " + placeholder
		}
		if f.DefValue != "" && f.DefValue != "false" {
			usage += p.Paint(term.Dim, " (default "+f.DefValue+")")
		}
		t.Row(p.Paint(term.Cyan, name), usage)
	})
	if t.Len() == 0 {
		return
	}
	p.Newline()
	p.Heading(title)
	t.Render()
}

func usageLine(cmd *command, path []string, fs *flag.FlagSet) string {
	parts := []string{path[0], "[global flags]"}
	parts = append(parts, path[1:]...)
	if len(cmd.sub) > 0 {
		parts = append(parts, "<command>")
	}
	if hasLocalFlags(fs) {
		parts = append(parts, "[flags]")
	}
	if cmd.args != "" {
		parts = append(parts, cmd.args)
	}
	return strings.Join(parts, " ")
}

func hasLocalFlags(fs *flag.FlagSet) bool {
	found := false
	if fs != nil {
		fs.VisitAll(func(f *flag.Flag) {
			if isLocalFlag(f.Name) {
				found = true
			}
		})
	}
	return found
}

func isHelp(s string) bool {
	switch s {
	case "help", "-h", "-help", "--help":
		return true
	}
	return false
}

func (a *app) config() (*config.Config, error) {
	if a.cfg == nil {
		cfg, err := config.Load(a.envFile, a.envFileSet)
		if err != nil {
			return nil, err
		}
		a.cfg = cfg
	}
	return a.cfg, nil
}

func (a *app) printJSON(v any) error {
	enc := json.NewEncoder(a.out.Writer())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func (a *app) confirm(ctx context.Context, question string, assumeYes bool) error {
	if assumeYes {
		return nil
	}
	if !term.IsTerminal(a.in) {
		return errors.New("confirmation required, rerun with --yes")
	}
	a.errp.Printf("%s %s %s ",
		a.errp.Paint(term.Yellow.With(term.Bold), "?"),
		question,
		a.errp.Paint(term.Dim, "[y/N]"),
	)

	type answer struct {
		line string
		err  error
	}
	ch := make(chan answer, 1)
	go func() {
		line, err := bufio.NewReader(a.in).ReadString('\n')
		ch <- answer{line: line, err: err}
	}()

	select {
	case <-ctx.Done():
		a.errp.Newline()
		return ctx.Err()
	case ans := <-ch:
		if ans.err != nil && !errors.Is(ans.err, io.EOF) {
			return ans.err
		}
		if !strings.HasSuffix(ans.line, "\n") {
			a.errp.Newline()
		}
		switch strings.ToLower(strings.TrimSpace(ans.line)) {
		case "y", "yes":
			return nil
		}
		return errAborted
	}
}
