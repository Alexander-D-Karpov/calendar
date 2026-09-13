package auth

import (
	"bytes"
	"context"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

var t0 = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

var _ Repository = (*fakeRepo)(nil)

type fakeRepo struct {
	mu          sync.Mutex
	users       map[domain.ID]domain.User
	sessions    map[domain.ID]domain.Session
	tokens      map[domain.ID]domain.APIToken
	identities  map[domain.ID]domain.Identity
	emailTokens map[domain.ID]domain.EmailToken
	audits      []domain.AuditEntry
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		users:       map[domain.ID]domain.User{},
		sessions:    map[domain.ID]domain.Session{},
		tokens:      map[domain.ID]domain.APIToken{},
		identities:  map[domain.ID]domain.Identity{},
		emailTokens: map[domain.ID]domain.EmailToken{},
	}
}

func (f *fakeRepo) CreateUser(_ context.Context, nu domain.NewUser) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if strings.EqualFold(u.Email, nu.Email) {
			return domain.User{}, domain.ErrConflict
		}
	}
	u := domain.User{
		ID: nu.ID, Email: nu.Email, EmailVerifiedAt: nu.EmailVerifiedAt, PasswordHash: nu.PasswordHash,
		DisplayName: nu.DisplayName, Timezone: nu.Timezone, CreatedAt: t0,
	}
	if nu.Identity != nil {
		if err := f.addIdentity(*nu.Identity, u.ID); err != nil {
			return domain.User{}, err
		}
	}
	f.users[u.ID] = u
	return u, nil
}

func (f *fakeRepo) addIdentity(id domain.Identity, user domain.ID) error {
	id.UserID = user
	for _, x := range f.identities {
		if (x.Provider == id.Provider && x.Subject == id.Subject) || (x.UserID == user && x.Provider == id.Provider) {
			return domain.ErrConflict
		}
	}
	f.identities[id.ID] = id
	return nil
}

func (f *fakeRepo) IdentityBySubject(_ context.Context, provider, subject string) (domain.Identity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, x := range f.identities {
		if x.Provider == provider && x.Subject == subject {
			return x, nil
		}
	}
	return domain.Identity{}, domain.ErrNotFound
}

func (f *fakeRepo) ListIdentities(_ context.Context, userID domain.ID) ([]domain.Identity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Identity
	for _, x := range f.identities {
		if x.UserID == userID {
			out = append(out, x)
		}
	}
	return out, nil
}

func (f *fakeRepo) CreateIdentity(_ context.Context, id domain.Identity) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addIdentity(id, id.UserID)
}

func (f *fakeRepo) DeleteIdentity(_ context.Context, userID domain.ID, provider string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, x := range f.identities {
		if x.UserID == userID && x.Provider == provider {
			delete(f.identities, id)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *fakeRepo) UserByID(_ context.Context, id domain.ID) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return u, nil
}

func (f *fakeRepo) UserByEmail(_ context.Context, email string) (domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if strings.EqualFold(u.Email, email) {
			return u, nil
		}
	}
	return domain.User{}, domain.ErrNotFound
}

func (f *fakeRepo) ListUsers(context.Context) ([]domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.User, 0, len(f.users))
	for _, u := range f.users {
		out = append(out, u)
	}
	return out, nil
}

func (f *fakeRepo) SetPasswordHash(_ context.Context, id domain.ID, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return domain.ErrNotFound
	}
	u.PasswordHash = hash
	f.users[id] = u
	return nil
}

func (f *fakeRepo) SetUserDisabled(_ context.Context, id domain.ID, at *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return domain.ErrNotFound
	}
	u.DisabledAt = at
	f.users[id] = u
	return nil
}

func (f *fakeRepo) CreateSession(_ context.Context, s domain.Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[s.ID] = s
	return nil
}

func (f *fakeRepo) SessionByTokenHash(_ context.Context, hash []byte) (domain.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.sessions {
		if bytes.Equal(s.TokenHash, hash) && !f.users[s.UserID].Disabled() {
			return s, nil
		}
	}
	return domain.Session{}, domain.ErrNotFound
}

func (f *fakeRepo) TouchSession(_ context.Context, id domain.ID, lastSeen, expires time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok {
		return domain.ErrNotFound
	}
	s.LastSeenAt, s.ExpiresAt = lastSeen, expires
	f.sessions[id] = s
	return nil
}

func (f *fakeRepo) SetSessionReauth(_ context.Context, id domain.ID, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok {
		return domain.ErrNotFound
	}
	s.ReauthAt = &at
	f.sessions[id] = s
	return nil
}

func (f *fakeRepo) DeleteSession(_ context.Context, userID, id domain.ID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok || s.UserID != userID {
		return domain.ErrNotFound
	}
	delete(f.sessions, id)
	return nil
}

func (f *fakeRepo) DeleteUserSessions(_ context.Context, userID, except domain.ID) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for id, s := range f.sessions {
		if s.UserID == userID && id != except {
			delete(f.sessions, id)
			n++
		}
	}
	return n, nil
}

func (f *fakeRepo) ListSessions(_ context.Context, userID domain.ID) ([]domain.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Session
	for _, s := range f.sessions {
		if s.UserID == userID {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeRepo) CreateAPIToken(_ context.Context, t domain.APIToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens[t.ID] = t
	return nil
}

func (f *fakeRepo) APITokenByPrefix(_ context.Context, prefix string) (domain.APIToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tokens {
		if t.Prefix == prefix && !f.users[t.UserID].Disabled() {
			return t, nil
		}
	}
	return domain.APIToken{}, domain.ErrNotFound
}

func (f *fakeRepo) TouchAPIToken(_ context.Context, id domain.ID, at time.Time, ip netip.Addr) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tokens[id]
	if !ok {
		return domain.ErrNotFound
	}
	t.LastUsedAt, t.LastUsedIP = &at, ip
	f.tokens[id] = t
	return nil
}

func (f *fakeRepo) ListAPITokens(_ context.Context, userID domain.ID) ([]domain.APIToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.APIToken
	for _, t := range f.tokens {
		if t.UserID == userID {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *fakeRepo) RevokeAPIToken(_ context.Context, userID, id domain.ID, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tokens[id]
	if !ok || t.UserID != userID || t.RevokedAt != nil {
		return domain.ErrNotFound
	}
	t.RevokedAt = &at
	f.tokens[id] = t
	return nil
}

func (f *fakeRepo) Audit(_ context.Context, e domain.AuditEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audits = append(f.audits, e)
	return nil
}

func (f *fakeRepo) actions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.audits))
	for i, a := range f.audits {
		out[i] = a.Action
	}
	return out
}

func (f *fakeRepo) CreateEmailToken(_ context.Context, t domain.EmailToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.emailTokens == nil {
		f.emailTokens = map[domain.ID]domain.EmailToken{}
	}
	f.emailTokens[t.ID] = t
	return nil
}

func (f *fakeRepo) ConsumeEmailToken(_ context.Context, purpose string, hash []byte, at time.Time) (domain.EmailToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, t := range f.emailTokens {
		if t.Purpose != purpose || !bytes.Equal(t.TokenHash, hash) || t.UsedAt != nil || !t.ExpiresAt.After(at) {
			continue
		}
		used := at
		t.UsedAt = &used
		f.emailTokens[id] = t
		return t, nil
	}
	return domain.EmailToken{}, domain.ErrNotFound
}

func (f *fakeRepo) DeleteEmailTokens(_ context.Context, userID domain.ID, purpose string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, t := range f.emailTokens {
		if t.UserID == userID && t.Purpose == purpose {
			delete(f.emailTokens, id)
		}
	}
	return nil
}

func (f *fakeRepo) SetEmailVerified(_ context.Context, id domain.ID, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok || u.EmailVerifiedAt != nil {
		return domain.ErrNotFound
	}
	u.EmailVerifiedAt = &at
	f.users[id] = u
	return nil
}
