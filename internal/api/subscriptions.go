package api

import (
	"net/http"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

type subscriptionJSON struct {
	ID            string     `json:"id"`
	CalendarID    string     `json:"calendar_id"`
	URL           string     `json:"url"`
	RefreshEvery  string     `json:"refresh_every"`
	Status        string     `json:"status"`
	NextRefreshAt time.Time  `json:"next_refresh_at"`
	LastOKAt      *time.Time `json:"last_ok_at"`
	LastError     string     `json:"last_error,omitempty"`
}

type subscriptionList struct {
	Items []subscriptionJSON `json:"items"`
}

type subscriptionInput struct {
	URL          string `json:"url"`
	Name         string `json:"name"`
	RefreshEvery string `json:"refresh_every"`
}

func listSubscriptions(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		rows, err := d.Subs.List(r.Context(), ownerID(r))
		if err != nil {
			return err
		}
		items := make([]subscriptionJSON, len(rows))
		for i, s := range rows {
			items[i] = subscriptionJSON{
				ID: s.ID.String(), CalendarID: s.CalendarID.String(), URL: s.URL, RefreshEvery: s.Interval.String(),
				Status: s.Status, NextRefreshAt: s.NextRefreshAt.UTC(), LastOKAt: utcPtr(s.LastOKAt), LastError: s.LastError,
			}
		}
		writeJSON(w, http.StatusOK, subscriptionList{Items: items})
		return nil
	}
}

func createSubscription(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		var in subscriptionInput
		if err := decodeJSON(r, &in); err != nil {
			return err
		}
		var every time.Duration
		if in.RefreshEvery != "" {
			parsed, err := time.ParseDuration(in.RefreshEvery)
			if err != nil {
				var v domain.ValidationError
				v.Add("refresh_every", "must be a duration like 1h or 30m")
				return v.Err()
			}
			every = parsed
		}
		sub, err := d.Subs.Create(r.Context(), ownerID(r), in.URL, in.Name, every)
		if err != nil {
			return err
		}
		w.Header().Set("Location", Prefix+"/subscriptions")
		writeJSON(w, http.StatusCreated, subscriptionJSON{
			ID: sub.ID.String(), CalendarID: sub.CalendarID.String(), URL: in.URL, RefreshEvery: sub.Interval.String(),
			Status: sub.Status, NextRefreshAt: sub.NextRefreshAt.UTC(),
		})
		return nil
	}
}

func refreshSubscription(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		if err := d.Subs.Refresh(r.Context(), ownerID(r), id); err != nil {
			return err
		}
		w.WriteHeader(http.StatusAccepted)
		return nil
	}
}

func deleteSubscription(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		if err := d.Subs.Delete(r.Context(), ownerID(r), id); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}
