package domain

import "time"

const (
	PurposeVerify = "verify"
	PurposeReset  = "reset"
)

type EmailToken struct {
	ID        ID
	UserID    ID
	Purpose   string
	TokenHash []byte
	NewEmail  string
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// IdempotentRecord is a stored reply to a POST that carried an Idempotency-Key.
// Body is empty while the original request is still in flight.
type IdempotentRecord struct {
	RequestHash []byte
	Status      int
	ContentType string
	Body        []byte
}
