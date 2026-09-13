package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestTee(t *testing.T) {
	var all, errs bytes.Buffer
	h := Tee(
		slog.NewTextHandler(&all, &slog.HandlerOptions{Level: slog.LevelDebug}),
		nil,
		slog.NewTextHandler(&errs, &slog.HandlerOptions{Level: slog.LevelError}),
	)
	logger := slog.New(h).With("component", "sync").WithGroup("req")
	logger.Info("pulling", "id", 1)
	logger.Error("failed", "id", 2)

	if n := strings.Count(all.String(), "\n"); n != 2 {
		t.Fatalf("all got %d lines: %q", n, all.String())
	}
	if strings.Contains(errs.String(), "pulling") || !strings.Contains(errs.String(), "component=sync req.id=2") {
		t.Fatalf("errs = %q", errs.String())
	}
	single := slog.NewTextHandler(&all, nil)
	if Tee(single, nil) != single {
		t.Fatal("Tee with one handler must return it unchanged")
	}
}
