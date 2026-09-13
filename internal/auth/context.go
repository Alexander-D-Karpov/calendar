package auth

import (
	"context"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

type Principal struct {
	UserID     domain.ID
	SessionID  domain.ID
	TokenID    domain.ID
	Scopes     []Scope
	CSRFSecret []byte
	ReauthAt   time.Time
	ExpiresAt  time.Time
	Refreshed  bool
}

func (p *Principal) IsSession() bool {
	return p != nil && p.SessionID != domain.NilID
}

func (p *Principal) Can(s Scope) bool {
	if p == nil {
		return false
	}
	if p.IsSession() {
		return true
	}
	return Allows(p.Scopes, s)
}

func (p *Principal) Fresh(now time.Time) bool {
	return p.IsSession() && Fresh(p.ReauthAt, now)
}

type principalKey struct{}

func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func PrincipalFrom(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}
