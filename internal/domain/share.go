package domain

import (
	"slices"
	"time"
)

const (
	DetailBusy   = "busy"
	DetailTitles = "titles"
	DetailFull   = "full"

	ShareTZOwner  = "owner"
	ShareTZViewer = "viewer"

	MaxShares = 100
)

var (
	ShareViews   = Views
	ShareDetails = []string{DetailBusy, DetailTitles, DetailFull}
	ShareTZModes = []string{ShareTZOwner, ShareTZViewer}
)

type Share struct {
	ID             ID
	OwnerID        ID
	Name           string
	Token          string
	TokenHash      []byte
	TokenEnc       []byte
	View           string
	Period         time.Time
	Detail         string
	IncludeTodos   bool
	ShowSleep      bool
	TZMode         string
	Calendars      []ID
	Version        int64
	AccessCount    int64
	LastAccessedAt *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	RevokedAt      *time.Time
}

func (s Share) ETag() string {
	return VersionTag(s.Version)
}

func (s Share) Active() bool {
	return s.RevokedAt == nil
}

func (s Share) Includes(cal ID) bool {
	return slices.Contains(s.Calendars, cal)
}

type SharePatch struct {
	Name         Opt[string]   `json:"name"`
	View         Opt[string]   `json:"view"`
	Period       Opt[string]   `json:"period"`
	Calendars    Opt[[]string] `json:"calendars"`
	Detail       Opt[string]   `json:"detail"`
	IncludeTodos Opt[bool]     `json:"include_todos"`
	ShowSleep    Opt[bool]     `json:"show_sleep"`
	TZMode       Opt[string]   `json:"tz_mode"`
}
