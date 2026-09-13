package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Alexander-D-Karpov/calendar/internal/term"
)

const defaultTimeFormat = "15:04:05.000"

type PrettyOptions struct {
	Level      slog.Leveler
	Color      bool
	TimeFormat string
}

type block struct {
	key  string
	text string
}

type PrettyHandler struct {
	opts   PrettyOptions
	w      io.Writer
	mu     *sync.Mutex
	fields []Field
	prefix string
}

func NewPrettyHandler(w io.Writer, opts PrettyOptions) *PrettyHandler {
	if opts.Level == nil {
		opts.Level = slog.LevelInfo
	}
	if opts.TimeFormat == "" {
		opts.TimeFormat = defaultTimeFormat
	}
	return &PrettyHandler{opts: opts, w: w, mu: &sync.Mutex{}}
}

func (h *PrettyHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.opts.Level.Level()
}

func (h *PrettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	c := *h
	c.fields = make([]Field, len(h.fields), len(h.fields)+len(attrs))
	copy(c.fields, h.fields)
	for _, a := range attrs {
		c.fields = Flatten(c.fields, h.prefix, a)
	}
	return &c
}

func (h *PrettyHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := *h
	c.prefix = JoinKey(h.prefix, name)
	return &c
}

func (h *PrettyHandler) Handle(_ context.Context, r slog.Record) error {
	fields := make([]Field, len(h.fields), len(h.fields)+r.NumAttrs())
	copy(fields, h.fields)
	r.Attrs(func(a slog.Attr) bool {
		fields = Flatten(fields, h.prefix, a)
		return true
	})

	var b strings.Builder
	if !r.Time.IsZero() {
		b.WriteString(h.paint(term.Dim, r.Time.Format(h.opts.TimeFormat)))
		b.WriteByte(' ')
	}
	b.WriteString(h.paint(levelStyle(r.Level), levelLabel(r.Level)))
	b.WriteByte(' ')
	b.WriteString(r.Message)

	var blocks []block
	for _, f := range fields {
		text := formatValue(f.Val)
		if strings.Contains(text, "\n") {
			blocks = append(blocks, block{key: f.Key, text: text})
			continue
		}
		b.WriteByte(' ')
		b.WriteString(h.paint(term.Dim, f.Key+"="))
		b.WriteString(h.paint(valueStyle(f), quote(text)))
	}
	b.WriteByte('\n')

	for _, bl := range blocks {
		b.WriteString("  ")
		b.WriteString(h.paint(term.Dim, bl.key))
		b.WriteByte('\n')
		for _, line := range strings.Split(strings.TrimRight(bl.text, "\n"), "\n") {
			b.WriteString("    ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *PrettyHandler) paint(s term.Style, text string) string {
	return s.Render(text, h.opts.Color)
}

func formatValue(v slog.Value) string {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindTime:
		return v.Time().Format(time.RFC3339Nano)
	case slog.KindDuration:
		return formatDuration(v.Duration())
	case slog.KindAny:
		switch x := v.Any().(type) {
		case error:
			return x.Error()
		case fmt.Stringer:
			return x.String()
		}
		return fmt.Sprintf("%+v", v.Any())
	}
	return v.String()
}

func formatDuration(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return d.Round(time.Microsecond).String()
	case d < time.Second:
		return d.Round(10 * time.Microsecond).String()
	}
	return d.Round(time.Millisecond).String()
}

func quote(s string) string {
	if s == "" {
		return `""`
	}
	for _, r := range s {
		if unicode.IsSpace(r) || r == '"' || r == '=' || !unicode.IsPrint(r) {
			return strconv.Quote(s)
		}
	}
	return s
}

func levelLabel(l slog.Level) string {
	s := l.String()
	if len(s) < 5 {
		s += strings.Repeat(" ", 5-len(s))
	}
	return s
}

func levelStyle(l slog.Level) term.Style {
	switch {
	case l >= slog.LevelError:
		return term.Red.With(term.Bold)
	case l >= slog.LevelWarn:
		return term.Yellow
	case l >= slog.LevelInfo:
		return term.Green
	}
	return term.Magenta
}

func valueStyle(f Field) term.Style {
	switch LastKey(f.Key) {
	case "error", "err":
		return term.Red
	case "status":
		if code, ok := statusCode(f.Val); ok {
			switch {
			case code >= 500:
				return term.Red
			case code >= 400:
				return term.Yellow
			case code >= 300:
				return term.Cyan
			case code >= 200:
				return term.Green
			}
		}
	}
	if f.Val.Kind() == slog.KindAny {
		if _, ok := f.Val.Any().(error); ok {
			return term.Red
		}
	}
	return term.Plain
}

func statusCode(v slog.Value) (int64, bool) {
	switch v.Kind() {
	case slog.KindInt64:
		return v.Int64(), true
	case slog.KindUint64:
		return int64(v.Uint64()), true
	}
	return 0, false
}
