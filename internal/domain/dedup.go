package domain

import "time"

const (
	DedupOff       = "off"
	DedupFlag      = "flag"
	DedupAutoExact = "auto_exact"
	DedupAutoPrint = "auto_fingerprint"

	ReasonUID         = "uid"
	ReasonFingerprint = "fingerprint"
	ReasonFuzzy       = "fuzzy"

	DupPending   = "pending"
	DupMerged    = "merged"
	DupDeleted   = "deleted"
	DupDismissed = "dismissed"

	EntityDuplicate = "duplicate"
	OriginDedup     = "dedup"
)

var DedupPolicies = []string{DedupOff, DedupFlag, DedupAutoExact, DedupAutoPrint}

type Duplicate struct {
	ID         ID
	OwnerID    ID
	Entity     string
	AID        ID
	BID        ID
	Reason     string
	Score      float64
	Status     string
	A          []byte
	B          []byte
	CreatedAt  time.Time
	ResolvedAt *time.Time
}

func (d Duplicate) Pending() bool {
	return d.Status == DupPending
}

func (d Duplicate) Auto(policy string) bool {
	switch policy {
	case DedupAutoPrint:
		return d.Reason == ReasonUID || d.Reason == ReasonFingerprint
	case DedupAutoExact:
		return d.Reason == ReasonUID
	}
	return false
}

type TrashItem struct {
	ID        ID
	Entity    string
	Title     string
	Where     string
	When      string
	DeletedAt time.Time
	Children  int
}
