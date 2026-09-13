//go:build integration

package pg

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/testutil/pgtest"
)

func TestAuthStore(t *testing.T) {
	ctx := context.Background()
	st := New(pgtest.New(t).Pool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	ip := netip.MustParseAddr("198.51.100.7")

	alice, err := st.CreateUser(ctx, domain.NewUser{ID: domain.NewID(), Email: "Alice@example.com", PasswordHash: "$argon2id$x", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser(ctx, domain.NewUser{ID: domain.NewID(), Email: "alice@EXAMPLE.com", Timezone: "UTC"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate email err = %v", err)
	}
	bob, err := st.CreateUser(ctx, domain.NewUser{ID: domain.NewID(), Email: "bob@example.com", Timezone: "UTC"})
	if err != nil || bob.HasPassword() {
		t.Fatalf("bob = %+v %v", bob, err)
	}
	if got, err := st.UserByEmail(ctx, "ALICE@example.com"); err != nil || got.ID != alice.ID {
		t.Fatalf("UserByEmail = %v %v", got.ID, err)
	}
	var cals, lists int
	if err := st.pool.QueryRow(ctx,
		`SELECT (SELECT count(*) FROM calendars WHERE owner_id = $1 AND is_default),
		        (SELECT count(*) FROM todo_lists WHERE owner_id = $1 AND is_default)`, alice.ID,
	).Scan(&cals, &lists); err != nil || cals != 1 || lists != 1 {
		t.Fatalf("defaults = %d %d %v", cals, lists, err)
	}

	sess := domain.Session{
		ID: domain.NewID(), UserID: alice.ID, TokenHash: crypto.HashToken("tok-a"), CSRFSecret: crypto.RandomBytes(32),
		IP: ip, UserAgent: "ua", CreatedAt: now, LastSeenAt: now,
		ExpiresAt: now.Add(time.Hour), AbsoluteExpiresAt: now.Add(24 * time.Hour),
	}
	if err := st.CreateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchSession(ctx, sess.ID, now.Add(time.Minute), now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionReauth(ctx, sess.ID, now); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.SessionByTokenHash(ctx, sess.TokenHash)
	if err != nil || loaded.IP != ip || loaded.ReauthAt == nil || !loaded.ExpiresAt.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("session = %+v %v", loaded, err)
	}
	if err := st.DeleteSession(ctx, bob.ID, sess.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign delete err = %v", err)
	}
	if list, _ := st.ListSessions(ctx, bob.ID); len(list) != 0 {
		t.Fatal("bob must not see alice's sessions")
	}

	tok := domain.APIToken{
		ID: domain.NewID(), UserID: alice.ID, Name: "cli", Prefix: "abcdefghijkl",
		SecretHash: crypto.HashToken("x"), Scopes: []string{"todos:read"}, CreatedAt: now,
	}
	if err := st.CreateAPIToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchAPIToken(ctx, tok.ID, now, ip); err != nil {
		t.Fatal(err)
	}
	got, err := st.APITokenByPrefix(ctx, tok.Prefix)
	if err != nil || got.LastUsedIP != ip || !slices.Equal(got.Scopes, tok.Scopes) {
		t.Fatalf("token = %+v %v", got, err)
	}
	if err := st.RevokeAPIToken(ctx, bob.ID, tok.ID, now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("foreign revoke err = %v", err)
	}
	if err := st.RevokeAPIToken(ctx, alice.ID, tok.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeAPIToken(ctx, alice.ID, tok.ID, now); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("second revoke err = %v", err)
	}

	if err := st.SetUserDisabled(ctx, alice.ID, &now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SessionByTokenHash(ctx, sess.TokenHash); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("disabled user session err = %v", err)
	}
	if _, err := st.APITokenByPrefix(ctx, tok.Prefix); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("disabled user token err = %v", err)
	}
	if n, err := st.DeleteUserSessions(ctx, alice.ID, domain.NilID); err != nil || n != 1 {
		t.Fatalf("DeleteUserSessions = %d %v", n, err)
	}
	if err := st.SetPasswordHash(ctx, domain.NewID(), "x"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing user err = %v", err)
	}
	if err := st.Audit(ctx, domain.AuditEntry{Action: "test", Meta: map[string]any{"k": "v"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Audit(ctx, domain.AuditEntry{UserID: alice.ID, Action: "test", IP: ip}); err != nil {
		t.Fatal(err)
	}
	if users, err := st.ListUsers(ctx); err != nil || len(users) != 2 {
		t.Fatalf("ListUsers = %d %v", len(users), err)
	}
}
