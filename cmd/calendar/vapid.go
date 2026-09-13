package main

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"flag"
)

type vapidReport struct {
	Public  string `json:"public"`
	Private string `json:"private"`
}

func vapidCommand() *command {
	return &command{
		name:    "vapid",
		summary: "Manage Web Push keys",
		sub: []*command{
			{name: "generate", summary: "Generate a VAPID key pair", setup: vapidGenerate},
		},
	}
}

func vapidGenerate(*flag.FlagSet) runFunc {
	return func(_ context.Context, a *app, args []string) error {
		if len(args) != 0 {
			return errUsage
		}
		key, err := ecdh.P256().GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		r := vapidReport{
			Public:  base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
			Private: base64.RawURLEncoding.EncodeToString(key.Bytes()),
		}
		if a.json {
			return a.printJSON(r)
		}
		a.out.Printf("WEBPUSH_VAPID_PUBLIC=%s\nWEBPUSH_VAPID_PRIVATE=%s\n", r.Public, r.Private)
		a.errp.Hint("also set WEBPUSH_VAPID_SUBJECT=mailto:you@example.com")
		return nil
	}
}
