package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"time"

	"golang.org/x/sync/errgroup"
)

const shutdownTimeout = 25 * time.Second

type namedServer struct {
	name string
	srv  *http.Server
}

func (a *App) Serve(ctx context.Context, handler http.Handler) error {
	servers := []namedServer{{name: "http", srv: a.httpServer(ctx, handler)}}
	if addr := a.Config.Metrics.Addr; addr != "" {
		ms := a.Metrics.Server(addr, a.Config.Metrics.Token)
		ms.ErrorLog = a.errorLog()
		servers = append(servers, namedServer{name: "metrics", srv: ms})
	}

	var lc net.ListenConfig
	listeners := make([]net.Listener, 0, len(servers))
	for _, s := range servers {
		ln, err := lc.Listen(ctx, "tcp", s.srv.Addr)
		if err != nil {
			for _, l := range listeners {
				_ = l.Close()
			}
			return fmt.Errorf("%s listener: %w", s.name, err)
		}
		listeners = append(listeners, ln)
	}

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		a.Listener.Run(gctx)
		return nil
	})
	g.Go(func() error {
		a.Notices.Run(gctx)
		return nil
	})
	if a.Worker != nil {
		g.Go(func() error {
			a.Worker.Run(gctx)
			return nil
		})
	}
	for i, s := range servers {
		ln := listeners[i]
		a.Logger.Info("listening", "server", s.name, "addr", ln.Addr().String())
		g.Go(func() error {
			if err := s.srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("%s server: %w", s.name, err)
			}
			return nil
		})
	}
	g.Go(func() error {
		<-gctx.Done()
		a.Logger.Info("shutting down", "timeout", shutdownTimeout)
		a.Hub.Close()
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		var errs []error
		for _, s := range servers {
			if err := s.srv.Shutdown(sctx); err != nil {
				errs = append(errs, fmt.Errorf("%s shutdown: %w", s.name, err))
			}
		}
		return errors.Join(errs...)
	})
	return g.Wait()
}

func (a *App) httpServer(ctx context.Context, h http.Handler) *http.Server {
	c := a.Config.HTTP
	return &http.Server{
		Addr:              c.Addr,
		Handler:           h,
		ReadHeaderTimeout: min(10*time.Second, c.ReadTimeout),
		ReadTimeout:       c.ReadTimeout,
		WriteTimeout:      c.WriteTimeout,
		IdleTimeout:       c.IdleTimeout,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          a.errorLog(),
		BaseContext: func(net.Listener) context.Context {
			return context.WithoutCancel(ctx)
		},
	}
}

func (a *App) errorLog() *log.Logger {
	return slog.NewLogLogger(a.Logger.Handler(), slog.LevelWarn)
}
