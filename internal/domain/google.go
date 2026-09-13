package domain

import "time"

const (
	OriginGoogle = "google"

	SyncBoth = "both"
	SyncPull = "pull"
	SyncPush = "push"

	BindCalendar = "calendar"
	BindTaskList = "tasklist"

	GoogleOK      = "ok"
	GoogleRevoked = "revoked"
	GoogleError   = "error"

	SyncUpsert = "upsert"
	SyncDelete = "delete"

	WinnerLocal  = "local"
	WinnerRemote = "remote"
)

var SyncDirections = []string{SyncBoth, SyncPull, SyncPush}

type GoogleAccount struct {
	ID              ID
	UserID          ID
	IdentityID      ID
	Email           string
	RefreshTokenEnc []byte
	Scopes          []string
	Status          string
	LastError       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (a GoogleAccount) Active() bool {
	return a.Status == GoogleOK
}

type SyncBinding struct {
	ID              ID
	OwnerID         ID
	AccountID       ID
	Entity          string
	CalendarID      *ID
	ListID          *ID
	RemoteID        string
	RemoteName      string
	RemoteAccess    string
	Direction       string
	Enabled         bool
	SyncToken       string
	UpdatedMin      *time.Time
	WatchChannelID  string
	WatchResourceID string
	WatchTokenHash  []byte
	WatchExpiresAt  *time.Time
	NextPollAt      time.Time
	LastSyncedAt    *time.Time
	LastError       string
}

func (b SyncBinding) Pulls() bool {
	return b.Enabled && b.Direction != SyncPush
}

func (b SyncBinding) Pushes() bool {
	return b.Enabled && b.Direction != SyncPull
}

func (b SyncBinding) Watching(now time.Time) bool {
	return b.WatchChannelID != "" && b.WatchExpiresAt != nil && b.WatchExpiresAt.After(now)
}

func (b SyncBinding) LocalID() ID {
	switch {
	case b.CalendarID != nil:
		return *b.CalendarID
	case b.ListID != nil:
		return *b.ListID
	}
	return NilID
}

type SyncMapping struct {
	BindingID     ID
	Entity        string
	LocalID       ID
	RemoteID      string
	RemoteETag    string
	RemoteUpdated *time.Time
	LocalVersion  int64
	RemoteDueDate *time.Time
}

type OutboxItem struct {
	ID        int64
	BindingID ID
	Entity    string
	LocalID   ID
	Op        string
	RemoteID  string
	Attempts  int
}

type SyncConflict struct {
	OwnerID   ID
	BindingID ID
	Entity    string
	LocalID   ID
	RemoteID  string
	Winner    string
	Local     any
	Remote    any
	CreatedAt time.Time
}
