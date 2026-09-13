package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/db"
)

const (
	KindCleanup      = "cleanup"
	KindReminders    = "reminders"
	KindGooglePull   = "google_pull"
	KindGooglePush   = "google_push"
	KindGooglePoll   = "google_poll"
	KindGoogleWatch  = "google_watch"
	KindSubscription = "subscription_refresh"
	KindSubsDue      = "subscription_poll"
	KindDedupScan    = "dedup_scan"
	KindDedupSweep   = "dedup_sweep"
	KindOGEvict      = "og_evict"

	defaultMaxAttempts = 10
)

const enqueueSQL = `INSERT INTO jobs (kind, payload, unique_key, run_at, priority, max_attempts)
	VALUES ($1, $2, $3, coalesce($4, now()), $5, $6)
	ON CONFLICT (kind, unique_key) WHERE unique_key IS NOT NULL AND status = 'queued' DO NOTHING`

type Job struct {
	ID          int64
	Kind        string
	Payload     []byte
	Attempts    int
	MaxAttempts int
}

func (j Job) Decode(v any) error {
	return json.Unmarshal(j.Payload, v)
}

type Handler func(ctx context.Context, j Job) error

type Options struct {
	UniqueKey   string
	RunAt       time.Time
	Priority    int
	MaxAttempts int
}

type permanentError struct {
	err error
}

func (p permanentError) Error() string {
	return p.err.Error()
}

func (p permanentError) Unwrap() error {
	return p.err
}

func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err: err}
}

func IsPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

func Enqueue(ctx context.Context, q db.DBTX, kind string, payload any, o Options) error {
	body := []byte("{}")
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = b
	}
	var key *string
	if o.UniqueKey != "" {
		key = &o.UniqueKey
	}
	var runAt *time.Time
	if !o.RunAt.IsZero() {
		t := o.RunAt.UTC()
		runAt = &t
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = defaultMaxAttempts
	}
	_, err := q.Exec(ctx, enqueueSQL, kind, body, key, runAt, int16(o.Priority), int32(o.MaxAttempts))
	return err
}
