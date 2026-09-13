package db

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

const maxListenBackoff = 30 * time.Second

func Listen(ctx context.Context, url, channel string, logger *slog.Logger, connected func(first bool), notify func(payload string)) {
	wait := time.Second
	first := true
	for {
		err := listenOnce(ctx, url, channel, func() {
			wait = time.Second
			connected(first)
			first = false
		}, notify)
		if ctx.Err() != nil {
			return
		}
		logger.LogAttrs(ctx, slog.LevelWarn, "listener disconnected",
			slog.String("channel", channel), slog.Any("err", err), slog.Duration("retry", wait))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, maxListenBackoff)
	}
}

func listenOnce(ctx context.Context, url, channel string, connected func(), notify func(string)) error {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = conn.Close(cctx)
	}()
	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
		return err
	}
	connected()
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		notify(n.Payload)
	}
}
