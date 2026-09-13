package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"

	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
)

type keyReport struct {
	ID    uint32 `json:"id"`
	Key   string `json:"key"`
	Entry string `json:"entry"`
}

func keysCommand() *command {
	return &command{
		name:    "keys",
		summary: "Manage encryption keys",
		sub: []*command{
			{name: "generate", summary: "Generate a new SECRET_KEYS entry", setup: keysGenerate},
		},
	}
}

func keysGenerate(*flag.FlagSet) runFunc {
	return func(_ context.Context, a *app, args []string) error {
		if len(args) != 0 {
			return errUsage
		}
		id := uint32(1)
		if cfg, err := a.config(); err == nil {
			id = cfg.Security.SecretKeys.Highest() + 1
		}
		key := base64.StdEncoding.EncodeToString(crypto.RandomBytes(crypto.KeySize))
		r := keyReport{ID: id, Key: key, Entry: fmt.Sprintf("%d:%s", id, key)}
		if a.json {
			return a.printJSON(r)
		}
		a.out.Println(r.Entry)
		if id == 1 {
			a.errp.Hint("set SECRET_KEYS=%s", r.Entry)
		} else {
			a.errp.Hint("append it to SECRET_KEYS, deploy, then set SECRET_KEY_ACTIVE=%d", id)
		}
		return nil
	}
}
