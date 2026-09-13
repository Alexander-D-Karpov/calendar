package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

const healthTimeout = 5 * time.Second

func healthcheckCommand() *command {
	return &command{
		name:    "healthcheck",
		summary: "Probe the local server's readiness endpoint and exit non-zero when it is not ready",
		setup:   healthcheckRun,
	}
}

func healthcheckRun(*flag.FlagSet) runFunc {
	return func(ctx context.Context, a *app, args []string) error {
		if len(args) != 0 {
			return errUsage
		}
		cfg, err := a.config()
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(ctx, healthTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+probeHost(cfg.HTTP.Addr)+"/readyz", nil)
		if err != nil {
			return err
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("not ready: %s", res.Status)
		}
		a.out.OK("ready")
		return nil
	}
}

// probeHost turns a listen address into one that can be dialled: HTTP_ADDR is
// usually ":8080", which is a valid bind but not a valid destination.
func probeHost(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return host + ":" + port
}
