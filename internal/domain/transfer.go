package domain

import "time"

const (
	SourceICS  = "ics"
	SourceCSV  = "csv"
	SourceYAML = "yaml"
	SourceURL  = "ics_url"

	ImportPreviewed  = "previewed"
	ImportCommitting = "committing"
	ImportDone       = "done"
	ImportFailed     = "failed"

	SubOK     = "ok"
	SubError  = "error"
	SubPaused = "paused"

	EntityImport       = "import"
	OriginImport       = "import"
	OriginSubscription = "subscription"
)

type Import struct {
	ID         ID
	OwnerID    ID
	Source     string
	Filename   string
	Status     string
	Payload    []byte
	PayloadSum []byte
	Options    []byte
	Preview    []byte
	Stats      []byte
	Error      string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ExpiresAt  time.Time
	FinishedAt *time.Time
}

type Subscription struct {
	ID            ID
	OwnerID       ID
	CalendarID    ID
	URLEnc        []byte
	Host          string
	Interval      time.Duration
	NextRefreshAt time.Time
	Status        string
	ETag          string
	LastModified  string
	ContentHash   []byte
	Failures      int
	LastAttemptAt *time.Time
	LastOKAt      *time.Time
	LastError     string
}
