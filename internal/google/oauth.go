package google

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	CallbackPath  = "/auth/google/callback"
	Issuer        = "https://accounts.google.com"
	authEndpoint  = "https://accounts.google.com/o/oauth2/v2/auth"
	tokenEndpoint = "https://oauth2.googleapis.com/token"
	jwksURL       = "https://www.googleapis.com/oauth2/v3/certs"
)

var LoginScopes = []string{oidc.ScopeOpenID, "email", "profile"}

type Claims struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	IssuedAt      time.Time
}

type AuthOptions struct {
	State         string
	Nonce         string
	Verifier      string
	LoginHint     string
	SelectAccount bool
	Scopes        []string
	Offline       bool
}

type OAuth struct {
	cfg      oauth2.Config
	client   *http.Client
	verifier *oidc.IDTokenVerifier
}

type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	switch strings.Trim(string(data), `"`) {
	case "true":
		*b = true
	case "false":
		*b = false
	default:
		return fmt.Errorf("google: invalid boolean %s", data)
	}
	return nil
}

func NewOAuth(clientID, secret, redirect string, client *http.Client) *OAuth {
	keys := oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), client), jwksURL)
	return &OAuth{
		cfg: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: secret,
			Endpoint:     oauth2.Endpoint{AuthURL: authEndpoint, TokenURL: tokenEndpoint, AuthStyle: oauth2.AuthStyleInParams},
			RedirectURL:  redirect,
			Scopes:       LoginScopes,
		},
		client:   client,
		verifier: oidc.NewVerifier(Issuer, keys, &oidc.Config{ClientID: clientID}),
	}
}

func NewVerifier() string {
	return oauth2.GenerateVerifier()
}

func (o *OAuth) AuthURL(a AuthOptions) string {
	cfg := o.cfg
	opts := []oauth2.AuthCodeOption{oidc.Nonce(a.Nonce), oauth2.S256ChallengeOption(a.Verifier)}
	if len(a.Scopes) > 0 {
		cfg.Scopes = append(slices.Clone(LoginScopes), a.Scopes...)
		opts = append(opts, oauth2.SetAuthURLParam("include_granted_scopes", "true"))
	}
	if a.LoginHint != "" {
		opts = append(opts, oauth2.SetAuthURLParam("login_hint", a.LoginHint))
	}
	var prompt []string
	if a.Offline {
		opts = append(opts, oauth2.AccessTypeOffline)
		prompt = append(prompt, "consent")
	}
	if a.SelectAccount {
		prompt = append(prompt, "select_account")
	}
	if len(prompt) > 0 {
		opts = append(opts, oauth2.SetAuthURLParam("prompt", strings.Join(prompt, " ")))
	}
	return cfg.AuthCodeURL(a.State, opts...)
}

func (o *OAuth) Exchange(ctx context.Context, code, verifier, nonce string) (Claims, *oauth2.Token, error) {
	if code == "" {
		return Claims{}, nil, errors.New("google: callback has no code")
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, o.client)
	tok, err := o.cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Claims{}, nil, fmt.Errorf("google: exchange: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return Claims{}, nil, errors.New("google: token response has no id_token")
	}
	idt, err := o.verifier.Verify(oidc.ClientContext(ctx, o.client), raw)
	if err != nil {
		return Claims{}, nil, fmt.Errorf("google: id token: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(nonce)) != 1 {
		return Claims{}, nil, errors.New("google: nonce mismatch")
	}
	var c struct {
		Email         string   `json:"email"`
		EmailVerified flexBool `json:"email_verified"`
		Name          string   `json:"name"`
	}
	if err := idt.Claims(&c); err != nil {
		return Claims{}, nil, fmt.Errorf("google: claims: %w", err)
	}
	return Claims{
		Subject:       idt.Subject,
		Email:         c.Email,
		EmailVerified: bool(c.EmailVerified),
		Name:          c.Name,
		IssuedAt:      idt.IssuedAt,
	}, tok, nil
}
