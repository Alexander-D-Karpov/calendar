package api

import (
	"net/http"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/gsync"
)

type syncBindingJSON struct {
	ID           string     `json:"id"`
	Entity       string     `json:"entity"`
	RemoteID     string     `json:"remote_id"`
	RemoteName   string     `json:"remote_name"`
	LocalID      string     `json:"local_id"`
	Direction    string     `json:"direction"`
	Enabled      bool       `json:"enabled"`
	Pending      int        `json:"pending"`
	Watching     bool       `json:"watching"`
	LastSyncedAt *time.Time `json:"last_synced_at"`
}

type syncStatusJSON struct {
	Connected bool              `json:"connected"`
	Email     string            `json:"email"`
	Status    string            `json:"status"`
	LastError string            `json:"last_error,omitempty"`
	Bindings  []syncBindingJSON `json:"bindings"`
}

func getSyncStatus(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if d.Sync == nil {
			return domain.ErrNotFound
		}
		st, err := d.Sync.Status(r.Context(), ownerID(r))
		if err != nil {
			return err
		}
		out := syncStatusJSON{Bindings: make([]syncBindingJSON, len(st.Bindings))}
		if st.Account != nil {
			out.Connected = st.Account.Active()
			out.Email = st.Account.Email
			out.Status = st.Account.Status
			out.LastError = st.Account.LastError
		}
		for i, b := range st.Bindings {
			out.Bindings[i] = toSyncBinding(b)
		}
		writeJSON(w, http.StatusOK, out)
		return nil
	}
}

func toSyncBinding(b gsync.BindingStatus) syncBindingJSON {
	out := syncBindingJSON{
		ID: b.ID.String(), Entity: b.Entity, RemoteID: b.RemoteID, RemoteName: b.RemoteName,
		Direction: b.Direction, Enabled: b.Enabled, Pending: b.Pending, Watching: b.Watching,
		LastSyncedAt: utcPtr(b.LastSyncedAt),
	}
	switch {
	case b.CalendarID != nil:
		out.LocalID = b.CalendarID.String()
	case b.ListID != nil:
		out.LocalID = b.ListID.String()
	}
	return out
}

// runSync queues a pull per enabled binding and returns; the work happens in the
// worker, so the response says accepted rather than done.
func runSync(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if d.Sync == nil {
			return domain.ErrNotFound
		}
		if err := d.Sync.Run(r.Context(), ownerID(r)); err != nil {
			return err
		}
		w.WriteHeader(http.StatusAccepted)
		return nil
	}
}
