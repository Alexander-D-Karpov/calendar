package api

import (
	"context"
	"net/http"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const (
	defaultChangeLimit = 200
	maxChangeLimit     = 1000
)

type ChangeStore interface {
	LatestSeq(ctx context.Context, owner domain.ID) (int64, error)
	ChangesSince(ctx context.Context, owner domain.ID, since int64, limit int) ([]domain.Change, error)
}

type changeJSON struct {
	Seq        int64      `json:"seq"`
	Entity     string     `json:"entity"`
	ID         string     `json:"id"`
	Op         string     `json:"op"`
	CalendarID *string    `json:"calendar_id"`
	ListID     *string    `json:"list_id"`
	From       *time.Time `json:"from"`
	To         *time.Time `json:"to"`
	Origin     string     `json:"origin,omitempty"`
}

type changeList struct {
	Items  []changeJSON `json:"items"`
	Latest int64        `json:"latest"`
}

func idString(id *domain.ID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
}

func listChanges(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		owner := ownerID(r)
		q := r.URL.Query()
		since, err := queryInt(q, "since", 0)
		if err != nil {
			return err
		}
		limit, err := queryInt(q, "limit", defaultChangeLimit)
		if err != nil {
			return err
		}
		limit = min(max(limit, 1), maxChangeLimit)
		since = max(since, 0)
		ctx := r.Context()
		rows, err := d.Changes.ChangesSince(ctx, owner, int64(since), limit)
		if err != nil {
			return err
		}
		latest, err := d.Changes.LatestSeq(ctx, owner)
		if err != nil {
			return err
		}
		items := make([]changeJSON, len(rows))
		for i, c := range rows {
			items[i] = changeJSON{
				Seq: c.Seq, Entity: c.Entity, ID: c.EntityID.String(), Op: c.Op,
				CalendarID: idString(c.CalendarID), ListID: idString(c.ListID),
				From: utcPtr(c.From), To: utcPtr(c.To), Origin: c.Origin,
			}
		}
		writeJSON(w, http.StatusOK, changeList{Items: items, Latest: latest})
		return nil
	}
}
