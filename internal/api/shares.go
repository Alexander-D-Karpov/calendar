package api

import (
	"net/http"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

type shareJSON struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	URL            string     `json:"url"`
	View           string     `json:"view"`
	Period         string     `json:"period"`
	Calendars      []string   `json:"calendars"`
	Detail         string     `json:"detail"`
	TZMode         string     `json:"tz_mode"`
	IncludeTodos   bool       `json:"include_todos"`
	ShowSleep      bool       `json:"show_sleep"`
	Active         bool       `json:"active"`
	AccessCount    int64      `json:"access_count"`
	LastAccessedAt *time.Time `json:"last_accessed_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type shareList struct {
	Items []shareJSON `json:"items"`
}

// toShareJSON exposes the address only while the share is usable: a revoked one
// keeps its row for the access log but its token must stop working.
func toShareJSON(s domain.Share, base string) shareJSON {
	cals := make([]string, len(s.Calendars))
	for i, id := range s.Calendars {
		cals[i] = id.String()
	}
	out := shareJSON{
		ID:             s.ID.String(),
		Name:           s.Name,
		View:           s.View,
		Period:         s.Period.Format(domain.DateLayout),
		Calendars:      cals,
		Detail:         s.Detail,
		TZMode:         s.TZMode,
		IncludeTodos:   s.IncludeTodos,
		ShowSleep:      s.ShowSleep,
		Active:         s.Active(),
		AccessCount:    s.AccessCount,
		LastAccessedAt: utcPtr(s.LastAccessedAt),
		CreatedAt:      s.CreatedAt.UTC(),
		UpdatedAt:      s.UpdatedAt.UTC(),
	}
	if s.Active() && s.Token != "" {
		out.URL = base + "/s/" + s.Token
	}
	return out
}

func listShares(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		rows, err := d.Shares.List(r.Context(), ownerID(r))
		if err != nil {
			return err
		}
		items := make([]shareJSON, len(rows))
		for i, s := range rows {
			items[i] = toShareJSON(s, d.BaseURL)
		}
		writeJSON(w, http.StatusOK, shareList{Items: items})
		return nil
	}
}

func createShare(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		var p domain.SharePatch
		if err := decodeJSON(r, &p, mediaJSON); err != nil {
			return err
		}
		s, err := d.Shares.Create(r.Context(), ownerID(r), p)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusCreated, toShareJSON(s, d.BaseURL))
		return nil
	}
}

func getShare(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		s, err := d.Shares.Get(r.Context(), ownerID(r), id)
		if err != nil {
			return err
		}
		writeResource(w, r, http.StatusOK, s.ETag(), toShareJSON(s, d.BaseURL))
		return nil
	}
}

func updateShare(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		var p domain.SharePatch
		if err := decodeJSON(r, &p, mediaMergePatch, mediaJSON); err != nil {
			return err
		}
		s, err := d.Shares.Update(r.Context(), ownerID(r), id, p, r.Header.Get("If-Match"))
		if err != nil {
			return err
		}
		writeResource(w, r, http.StatusOK, s.ETag(), toShareJSON(s, d.BaseURL))
		return nil
	}
}

func regenerateShare(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		s, err := d.Shares.Regenerate(r.Context(), ownerID(r), id)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, toShareJSON(s, d.BaseURL))
		return nil
	}
}

func revokeShare(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		if err := d.Shares.Revoke(r.Context(), ownerID(r), id); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}
