package domain

import (
	"net/netip"
	"strings"
	"time"
)

type User struct {
	ID               ID
	Email            string
	EmailVerifiedAt  *time.Time
	PasswordHash     string
	DisplayName      string
	Timezone         string
	WeekStart        int
	TimeFormat       string
	DefaultView      string
	SleepEnabled     bool
	DedupPolicy      string
	DateOnlyReminder int
	CreatedAt        time.Time
	UpdatedAt        time.Time
	DisabledAt       *time.Time
}

func (u User) ETag() string {
	return TimeTag(u.UpdatedAt)
}

func (u User) Location() *time.Location {
	if u.Timezone != "" {
		if loc, err := time.LoadLocation(u.Timezone); err == nil {
			return loc
		}
	}
	return time.UTC
}

func (u User) Disabled() bool {
	return u.DisabledAt != nil
}

func (u User) Verified() bool {
	return u.EmailVerifiedAt != nil
}

func (u User) HasPassword() bool {
	return u.PasswordHash != ""
}

func (u User) Name() string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	local, _, _ := strings.Cut(u.Email, "@")
	return local
}

const ProviderGoogle = "google"

type Identity struct {
	ID       ID
	UserID   ID
	Provider string
	Subject  string
	Email    string
	LinkedAt time.Time
}

type NewUser struct {
	ID              ID
	Email           string
	EmailVerifiedAt *time.Time
	PasswordHash    string
	DisplayName     string
	Timezone        string
	Identity        *Identity
}

type Session struct {
	ID                ID
	UserID            ID
	TokenHash         []byte
	CSRFSecret        []byte
	IP                netip.Addr
	UserAgent         string
	CreatedAt         time.Time
	LastSeenAt        time.Time
	ExpiresAt         time.Time
	AbsoluteExpiresAt time.Time
	ReauthAt          *time.Time
}

type APIToken struct {
	ID         ID
	UserID     ID
	Name       string
	Prefix     string
	SecretHash []byte
	Scopes     []string
	CreatedAt  time.Time
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	LastUsedIP netip.Addr
	RevokedAt  *time.Time
}

func (t APIToken) Active(now time.Time) bool {
	return t.RevokedAt == nil && (t.ExpiresAt == nil || now.Before(*t.ExpiresAt))
}

type AuditEntry struct {
	UserID    ID
	Action    string
	IP        netip.Addr
	UserAgent string
	Meta      map[string]any
}
