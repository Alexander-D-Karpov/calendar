package api

import (
	"net/http"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/service"
)

type eventJSON struct {
	ID           string    `json:"id"`
	CalendarID   string    `json:"calendar_id"`
	UID          string    `json:"uid"`
	SeriesID     *string   `json:"series_id"`
	Instance     *string   `json:"instance"`
	Recurring    bool      `json:"recurring"`
	Title        string    `json:"title"`
	Body         string    `json:"body"`
	Location     string    `json:"location"`
	URL          string    `json:"url"`
	AllDay       bool      `json:"all_day"`
	Start        string    `json:"start"`
	End          string    `json:"end"`
	Timezone     *string   `json:"timezone"`
	RRule        *string   `json:"rrule"`
	RDate        []string  `json:"rdate"`
	ExDate       []string  `json:"exdate"`
	Status       string    `json:"status"`
	Transparency string    `json:"transparency"`
	Visibility   string    `json:"visibility"`
	Color        *string   `json:"color"`
	ReadOnly     bool      `json:"read_only"`
	Reminders    []int     `json:"reminders"`
	Version      int64     `json:"version"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type eventList struct {
	Items []eventJSON `json:"items"`
}

func eventTime(e domain.Event, t time.Time) string {
	if e.AllDay {
		return t.UTC().Format(domain.DateLayout)
	}
	return t.In(e.Zone()).Format(time.RFC3339)
}

func eventTimes(e domain.Event, ts []time.Time) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = eventTime(e, t)
	}
	return out
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func toEventJSON(o domain.Occurrence) eventJSON {
	e := o.Event
	out := eventJSON{
		ID:           e.ID.String(),
		CalendarID:   e.CalendarID.String(),
		UID:          e.UID,
		Recurring:    e.IsMaster() || e.IsOverride(),
		Title:        e.Title,
		Body:         e.Body,
		Location:     e.Location,
		URL:          e.URL,
		AllDay:       e.AllDay,
		Start:        eventTime(e, e.Start),
		End:          eventTime(e, e.End),
		RRule:        optString(e.RRule),
		RDate:        eventTimes(e, e.RDate),
		ExDate:       eventTimes(e, e.ExDate),
		Status:       e.Status,
		Transparency: e.Transparency,
		Visibility:   e.Visibility,
		Color:        optString(e.Color),
		ReadOnly:     e.ReadOnly,
		Reminders:    e.Reminders,
		Version:      e.Version,
		CreatedAt:    e.CreatedAt.UTC(),
		UpdatedAt:    e.UpdatedAt.UTC(),
	}
	if e.SeriesID != nil {
		out.SeriesID = optString(e.SeriesID.String())
	}
	if o.Instance != nil {
		out.Instance = optString(eventTime(e, *o.Instance))
	}
	if !e.AllDay {
		out.Timezone = optString(e.TZ)
	}
	if out.Reminders == nil {
		out.Reminders = []int{}
	}
	return out
}

func eventResponse(e domain.Event) eventJSON {
	return toEventJSON(domain.Occurrence{Event: e, Instance: e.RecurrenceID})
}

func toEventList(list []domain.Occurrence) eventList {
	items := make([]eventJSON, len(list))
	for i, o := range list {
		items[i] = toEventJSON(o)
	}
	return eventList{Items: items}
}

func writeEventList(w http.ResponseWriter, list []domain.Occurrence) {
	writeJSON(w, http.StatusOK, toEventList(list))
}

func editFrom(r *http.Request) service.Edit {
	q := r.URL.Query()
	return service.Edit{Scope: q.Get("scope"), Instance: q.Get("instance"), IfMatch: r.Header.Get("If-Match")}
}

func listEvents(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		q := r.URL.Query()
		expand, err := queryBool(q, "expand", true)
		if err != nil {
			return err
		}
		list, err := d.Events.List(r.Context(), ownerID(r), service.EventQuery{
			From:      q.Get("from"),
			To:        q.Get("to"),
			Calendars: queryList(q, "calendar_id"),
			Expand:    expand,
		})
		if err != nil {
			return err
		}
		writeEventList(w, list)
		return nil
	}
}

func listEventInstances(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		q := r.URL.Query()
		list, err := d.Events.Instances(r.Context(), ownerID(r), id, q.Get("from"), q.Get("to"))
		if err != nil {
			return err
		}
		writeEventList(w, list)
		return nil
	}
}

func getEvent(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		e, err := d.Events.Get(r.Context(), ownerID(r), id)
		if err != nil {
			return err
		}
		writeResource(w, r, http.StatusOK, e.ETag(), eventResponse(e))
		return nil
	}
}

func createEvent(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		var p domain.EventPatch
		if err := decodeJSON(r, &p); err != nil {
			return err
		}
		e, err := d.Events.Create(r.Context(), ownerID(r), p)
		if err != nil {
			return err
		}
		w.Header().Set("Location", Prefix+"/events/"+e.ID.String())
		writeResource(w, r, http.StatusCreated, e.ETag(), eventResponse(e))
		return nil
	}
}

func updateEvent(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		var p domain.EventPatch
		if err := decodeJSON(r, &p, mediaMergePatch, mediaJSON); err != nil {
			return err
		}
		e, err := d.Events.Update(r.Context(), ownerID(r), id, p, editFrom(r))
		if err != nil {
			return err
		}
		writeResource(w, r, http.StatusOK, e.ETag(), eventResponse(e))
		return nil
	}
}

func deleteEvent(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		if err := d.Events.Delete(r.Context(), ownerID(r), id, editFrom(r)); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}
