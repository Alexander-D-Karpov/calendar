package logging

import (
	"context"
	"errors"
	"log/slog"
	"slices"
)

type teeHandler struct {
	handlers []slog.Handler
}

func Tee(handlers ...slog.Handler) slog.Handler {
	hs := slices.DeleteFunc(slices.Clone(handlers), func(h slog.Handler) bool { return h == nil })
	if len(hs) == 1 {
		return hs[0]
	}
	return &teeHandler{handlers: hs}
}

func (t *teeHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range t.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (t *teeHandler) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range t.handlers {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		if err := h.Handle(ctx, r.Clone()); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (t *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Handler, len(t.handlers))
	for i, h := range t.handlers {
		out[i] = h.WithAttrs(attrs)
	}
	return &teeHandler{handlers: out}
}

func (t *teeHandler) WithGroup(name string) slog.Handler {
	out := make([]slog.Handler, len(t.handlers))
	for i, h := range t.handlers {
		out[i] = h.WithGroup(name)
	}
	return &teeHandler{handlers: out}
}
