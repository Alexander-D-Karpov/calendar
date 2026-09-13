package logging

import (
	"io"
	"log/slog"
	"os"

	"github.com/Alexander-D-Karpov/calendar/internal/term"
)

const (
	FormatAuto   = "auto"
	FormatJSON   = "json"
	FormatText   = "text"
	FormatPretty = "pretty"
)

type Options struct {
	Level  slog.Leveler
	Format string
	Color  term.ColorMode
	Writer io.Writer
}

func New(o Options) *slog.Logger {
	return slog.New(NewHandler(o))
}

func NewHandler(o Options) slog.Handler {
	w := o.Writer
	if w == nil {
		w = os.Stderr
	}
	level := o.Level
	if level == nil {
		level = slog.LevelInfo
	}
	switch ResolveFormat(o.Format, w) {
	case FormatJSON:
		return slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	case FormatText:
		return slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	}
	return NewPrettyHandler(w, PrettyOptions{
		Level: level,
		Color: term.ColorEnabled(w, o.Color),
	})
}

func ResolveFormat(format string, w io.Writer) string {
	switch format {
	case FormatJSON, FormatText, FormatPretty:
		return format
	}
	if term.IsTerminal(w) {
		return FormatPretty
	}
	return FormatJSON
}
