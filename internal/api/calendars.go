package api

import (
	"net/http"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

type calendarJSON struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Color            string    `json:"color"`
	Description      string    `json:"description"`
	Timezone         *string   `json:"timezone"`
	Kind             string    `json:"kind"`
	ReadOnly         bool      `json:"read_only"`
	IsDefault        bool      `json:"is_default"`
	Hidden           bool      `json:"hidden"`
	Position         int       `json:"position"`
	DefaultReminders []int     `json:"default_reminders"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type calendarList struct {
	Items []calendarJSON `json:"items"`
}

func toCalendarJSON(c domain.Calendar) calendarJSON {
	out := calendarJSON{
		ID:               c.ID.String(),
		Name:             c.Name,
		Color:            c.Color,
		Description:      c.Description,
		Kind:             c.Kind,
		ReadOnly:         c.ReadOnly,
		IsDefault:        c.IsDefault,
		Hidden:           c.Hidden,
		Position:         c.Position,
		DefaultReminders: c.DefaultReminders,
		CreatedAt:        c.CreatedAt.UTC(),
		UpdatedAt:        c.UpdatedAt.UTC(),
	}
	if c.Timezone != "" {
		tz := c.Timezone
		out.Timezone = &tz
	}
	if out.DefaultReminders == nil {
		out.DefaultReminders = []int{}
	}
	return out
}

func toCalendarList(list []domain.Calendar) calendarList {
	items := make([]calendarJSON, len(list))
	for i, c := range list {
		items[i] = toCalendarJSON(c)
	}
	return calendarList{Items: items}
}

func listCalendars(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		list, err := d.Calendars.List(r.Context(), ownerID(r))
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, toCalendarList(list))
		return nil
	}
}

func getCalendar(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		c, err := d.Calendars.Get(r.Context(), ownerID(r), id)
		if err != nil {
			return err
		}
		writeResource(w, r, http.StatusOK, c.ETag(), toCalendarJSON(c))
		return nil
	}
}

func createCalendar(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		var p domain.CalendarPatch
		if err := decodeJSON(r, &p); err != nil {
			return err
		}
		c, err := d.Calendars.Create(r.Context(), ownerID(r), p)
		if err != nil {
			return err
		}
		w.Header().Set("Location", Prefix+"/calendars/"+c.ID.String())
		writeResource(w, r, http.StatusCreated, c.ETag(), toCalendarJSON(c))
		return nil
	}
}

func updateCalendar(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		var p domain.CalendarPatch
		if err := decodeJSON(r, &p, mediaMergePatch, mediaJSON); err != nil {
			return err
		}
		c, err := d.Calendars.Update(r.Context(), ownerID(r), id, p, r.Header.Get("If-Match"))
		if err != nil {
			return err
		}
		writeResource(w, r, http.StatusOK, c.ETag(), toCalendarJSON(c))
		return nil
	}
}

func deleteCalendar(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		if err := d.Calendars.Delete(r.Context(), ownerID(r), id, r.Header.Get("If-Match")); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}
