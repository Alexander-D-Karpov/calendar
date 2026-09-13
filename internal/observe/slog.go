package observe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/Alexander-D-Karpov/calendar/internal/logging"
)

const (
	maxErrorDepth = 10
	maxFrames     = 100
)

var tagKeys = map[string]bool{
	"component":  true,
	"request_id": true,
	"route":      true,
	"job":        true,
	"kind":       true,
}

var internalFrames = []string{
	"/internal/observe.(*slogHandler).",
	"/internal/observe.buildEvent",
	"/internal/observe.stacktrace",
	"/internal/logging.(*teeHandler).",
}

type slogHandler struct {
	level  slog.Leveler
	crumbs slog.Leveler
	fields []logging.Field
	prefix string
}

func NewSlogHandler(level slog.Leveler) slog.Handler {
	if level == nil {
		level = slog.LevelError
	}
	return &slogHandler{level: level, crumbs: slog.LevelInfo}
}

func (h *slogHandler) Enabled(ctx context.Context, l slog.Level) bool {
	if l >= h.level.Level() {
		return true
	}
	return l >= h.crumbs.Level() && sentry.GetHubFromContext(ctx) != nil
}

func (h *slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	c := *h
	c.fields = slices.Clone(h.fields)
	for _, a := range attrs {
		c.fields = logging.Flatten(c.fields, h.prefix, a)
	}
	return &c
}

func (h *slogHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := *h
	c.prefix = logging.JoinKey(h.prefix, name)
	return &c
}

func (h *slogHandler) Handle(ctx context.Context, r slog.Record) error {
	fields := slices.Clone(h.fields)
	r.Attrs(func(a slog.Attr) bool {
		fields = logging.Flatten(fields, h.prefix, a)
		return true
	})
	hub := sentry.GetHubFromContext(ctx)
	if r.Level < h.level.Level() {
		if hub != nil {
			hub.AddBreadcrumb(&sentry.Breadcrumb{
				Type:      "default",
				Category:  "log",
				Message:   r.Message,
				Level:     sentryLevel(r.Level),
				Timestamp: r.Time,
				Data:      breadcrumbData(fields),
			}, nil)
		}
		return nil
	}
	if hub == nil {
		hub = sentry.CurrentHub()
	}
	if hub.Client() == nil {
		return nil
	}
	hub.CaptureEvent(buildEvent(r, fields))
	return nil
}

func buildEvent(r slog.Record, fields []logging.Field) *sentry.Event {
	e := sentry.NewEvent()
	if e.Contexts == nil {
		e.Contexts = map[string]sentry.Context{}
	}
	if e.Tags == nil {
		e.Tags = map[string]string{}
	}
	e.Level = sentryLevel(r.Level)
	e.Message = r.Message
	e.Logger = "slog"
	if !r.Time.IsZero() {
		e.Timestamp = r.Time
	}

	attrs := sentry.Context{}
	var cause error
	var panicVal any
	hasPanic := false
	for _, f := range fields {
		base := logging.LastKey(f.Key)
		switch {
		case base == "stack":
		case base == "panic" && !hasPanic:
			hasPanic, panicVal = true, f.Val.Any()
		case (base == "err" || base == "error") && cause == nil && isError(f.Val):
			cause = f.Val.Any().(error)
		case tagKeys[base]:
			e.Tags[base] = f.Val.String()
		default:
			attrs[f.Key] = plain(f.Val)
		}
	}
	if len(attrs) > 0 {
		e.Contexts["log"] = attrs
	}

	st := stacktrace(r.PC)
	switch {
	case hasPanic:
		e.Level = sentry.LevelFatal
		e.Exception = []sentry.Exception{{Type: "panic", Value: fmt.Sprint(panicVal), Stacktrace: st}}
	case cause != nil:
		e.Exception = exceptionChain(cause, st)
	default:
		e.Threads = []sentry.Thread{{Stacktrace: st, Current: true}}
	}
	return e
}

func exceptionChain(err error, st *sentry.Stacktrace) []sentry.Exception {
	var out []sentry.Exception
	for e := err; e != nil && len(out) < maxErrorDepth; e = errors.Unwrap(e) {
		out = append(out, sentry.Exception{Type: reflect.TypeOf(e).String(), Value: e.Error()})
	}
	slices.Reverse(out)
	out[len(out)-1].Stacktrace = st
	return out
}

func stacktrace(caller uintptr) *sentry.Stacktrace {
	pcs := make([]uintptr, maxFrames)
	n := runtime.Callers(2, pcs)
	iter := runtime.CallersFrames(pcs[:n])
	var newestFirst []runtime.Frame
	panicked := false
	for {
		fr, more := iter.Next()
		switch {
		case fr.Function == "runtime.gopanic":
			newestFirst = newestFirst[:0]
			panicked = true
		case strings.HasPrefix(fr.Function, "runtime."):
		default:
			newestFirst = append(newestFirst, fr)
		}
		if !more {
			break
		}
	}
	if !panicked {
		newestFirst = trimToCaller(newestFirst, caller)
	}
	if len(newestFirst) == 0 {
		return nil
	}
	frames := make([]sentry.Frame, len(newestFirst))
	for i, fr := range newestFirst {
		frames[len(frames)-1-i] = sentry.NewFrame(fr)
	}
	return &sentry.Stacktrace{Frames: frames}
}

func trimToCaller(frames []runtime.Frame, pc uintptr) []runtime.Frame {
	if pc != 0 {
		c, _ := runtime.CallersFrames([]uintptr{pc}).Next()
		for i, fr := range frames {
			if fr.Function == c.Function && fr.Line == c.Line {
				return frames[i:]
			}
		}
	}
	for i, fr := range frames {
		if !internalFrame(fr.Function) {
			return frames[i:]
		}
	}
	return nil
}

func internalFrame(fn string) bool {
	if strings.HasPrefix(fn, "log/slog.") {
		return true
	}
	for _, s := range internalFrames {
		if strings.Contains(fn, s) {
			return true
		}
	}
	return false
}

func isError(v slog.Value) bool {
	if v.Kind() != slog.KindAny {
		return false
	}
	_, ok := v.Any().(error)
	return ok
}

func plain(v slog.Value) any {
	switch v.Kind() {
	case slog.KindString:
		return v.String()
	case slog.KindInt64:
		return v.Int64()
	case slog.KindUint64:
		return v.Uint64()
	case slog.KindFloat64:
		return v.Float64()
	case slog.KindBool:
		return v.Bool()
	case slog.KindDuration:
		return v.Duration().String()
	case slog.KindTime:
		return v.Time().Format(time.RFC3339Nano)
	}
	switch x := v.Any().(type) {
	case error:
		return x.Error()
	case fmt.Stringer:
		return x.String()
	}
	return fmt.Sprintf("%+v", v.Any())
}

func breadcrumbData(fields []logging.Field) map[string]any {
	if len(fields) == 0 {
		return nil
	}
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		if logging.LastKey(f.Key) != "stack" {
			out[f.Key] = plain(f.Val)
		}
	}
	return out
}

func sentryLevel(l slog.Level) sentry.Level {
	switch {
	case l >= slog.LevelError+4:
		return sentry.LevelFatal
	case l >= slog.LevelError:
		return sentry.LevelError
	case l >= slog.LevelWarn:
		return sentry.LevelWarning
	case l >= slog.LevelInfo:
		return sentry.LevelInfo
	}
	return sentry.LevelDebug
}
