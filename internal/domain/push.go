package domain

import "time"

type PushSubscription struct {
	ID        ID
	UserID    ID
	Endpoint  string
	P256dh    string
	Auth      string
	UserAgent string
	CreatedAt time.Time
	LastOKAt  *time.Time
	Failures  int
}

type Reminder struct {
	OwnerID       ID
	Entity        string
	EntityID      ID
	OccurrenceAt  time.Time
	MinutesBefore int
	FireAt        time.Time
}
