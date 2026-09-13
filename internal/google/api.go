package google

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"google.golang.org/api/tasks/v1"
)

const (
	revokeEndpoint = "https://oauth2.googleapis.com/revoke"
	apiTimeout     = 30 * time.Second
	watchTTL       = 7 * 24 * time.Hour
)

var SyncScopes = []string{calendar.CalendarScope, tasks.TasksScope}

func HasSyncScopes(granted []string) bool {
	for _, s := range SyncScopes {
		if !slices.Contains(granted, s) {
			return false
		}
	}
	return true
}

type api struct {
	cal   *calendar.Service
	tasks *tasks.Service
}

func (o *OAuth) Remote(ctx context.Context, refresh string) (Remote, error) {
	base := context.WithValue(ctx, oauth2.HTTPClient, o.client)
	hc := oauth2.NewClient(base, o.cfg.TokenSource(base, &oauth2.Token{RefreshToken: refresh}))
	hc.Timeout = apiTimeout
	c, err := calendar.NewService(ctx, option.WithHTTPClient(hc))
	if err != nil {
		return nil, fmt.Errorf("google: calendar client: %w", err)
	}
	t, err := tasks.NewService(ctx, option.WithHTTPClient(hc))
	if err != nil {
		return nil, fmt.Errorf("google: tasks client: %w", err)
	}
	return &api{cal: c, tasks: t}, nil
}

func (o *OAuth) Revoke(ctx context.Context, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, revokeEndpoint, strings.NewReader(url.Values{"token": {token}}.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := o.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("google: revoke returned %d", resp.StatusCode)
	}
	return nil
}

func apiErr(err error) error {
	if err == nil {
		return nil
	}
	var re *oauth2.RetrieveError
	if errors.As(err, &re) && re.ErrorCode == "invalid_grant" {
		return fmt.Errorf("%w: %s", ErrRevoked, re.ErrorDescription)
	}
	var ge *googleapi.Error
	if errors.As(err, &ge) {
		switch ge.Code {
		case http.StatusUnauthorized:
			return fmt.Errorf("%w: %w", ErrRevoked, err)
		case http.StatusNotFound:
			return fmt.Errorf("%w: %w", ErrNotFound, err)
		case http.StatusGone:
			return fmt.Errorf("%w: %w", ErrGone, err)
		case http.StatusPreconditionFailed:
			return fmt.Errorf("%w: %w", ErrPrecondition, err)
		}
	}
	return err
}

func (a *api) Calendars(ctx context.Context) ([]Calendar, error) {
	var out []Calendar
	err := a.cal.CalendarList.List().MinAccessRole("reader").ShowHidden(true).Pages(ctx, func(p *calendar.CalendarList) error {
		for _, c := range p.Items {
			if c.Deleted {
				continue
			}
			name := c.SummaryOverride
			if name == "" {
				name = c.Summary
			}
			out = append(out, Calendar{ID: c.Id, Name: name, Color: c.BackgroundColor, AccessRole: c.AccessRole, TimeZone: c.TimeZone, Primary: c.Primary})
		}
		return nil
	})
	return out, apiErr(err)
}

func (a *api) TaskLists(ctx context.Context) ([]TaskList, error) {
	var out []TaskList
	err := a.tasks.Tasklists.List().MaxResults(100).Pages(ctx, func(p *tasks.TaskLists) error {
		for _, l := range p.Items {
			out = append(out, TaskList{ID: l.Id, Name: l.Title})
		}
		return nil
	})
	return out, apiErr(err)
}

func (a *api) Events(ctx context.Context, cal, token string) (EventDelta, error) {
	call := a.cal.Events.List(cal).MaxResults(2500).ShowDeleted(true)
	if token != "" {
		call = call.SyncToken(token)
	}
	var d EventDelta
	err := call.Pages(ctx, func(p *calendar.Events) error {
		for _, e := range p.Items {
			d.Items = append(d.Items, fromEvent(e, cal))
		}
		if p.NextSyncToken != "" {
			d.SyncToken = p.NextSyncToken
		}
		return nil
	})
	return d, apiErr(err)
}

func (a *api) Event(ctx context.Context, cal, id string) (Event, error) {
	e, err := a.cal.Events.Get(cal, id).Context(ctx).Do()
	if err != nil {
		return Event{}, apiErr(err)
	}
	return fromEvent(e, cal), nil
}

func (a *api) Instance(ctx context.Context, cal, master, originalStart string) (Event, error) {
	res, err := a.cal.Events.Instances(cal, master).OriginalStart(originalStart).MaxResults(1).Context(ctx).Do()
	if err != nil {
		return Event{}, apiErr(err)
	}
	if len(res.Items) == 0 {
		return Event{}, ErrNotFound
	}
	return fromEvent(res.Items[0], cal), nil
}

func (a *api) InsertEvent(ctx context.Context, cal string, e Event) (Event, error) {
	out, err := a.cal.Events.Insert(cal, toEvent(e)).Context(ctx).Do()
	if err != nil {
		return Event{}, apiErr(err)
	}
	return fromEvent(out, cal), nil
}

func (a *api) PatchEvent(ctx context.Context, cal string, e Event, ifMatch string) (Event, error) {
	call := a.cal.Events.Patch(cal, e.ID, toEvent(e))
	if ifMatch != "" {
		call.Header().Set("If-Match", ifMatch)
	}
	out, err := call.Context(ctx).Do()
	if err != nil {
		return Event{}, apiErr(err)
	}
	return fromEvent(out, cal), nil
}

func (a *api) DeleteEvent(ctx context.Context, cal, id string) error {
	return apiErr(a.cal.Events.Delete(cal, id).Context(ctx).Do())
}

func (a *api) Watch(ctx context.Context, cal, channel, token, address string) (string, time.Time, error) {
	ch, err := a.cal.Events.Watch(cal, &calendar.Channel{
		Id:         channel,
		Type:       "web_hook",
		Address:    address,
		Token:      token,
		Expiration: time.Now().Add(watchTTL).UnixMilli(),
	}).Context(ctx).Do()
	if err != nil {
		return "", time.Time{}, apiErr(err)
	}
	return ch.ResourceId, time.UnixMilli(ch.Expiration).UTC(), nil
}

func (a *api) StopWatch(ctx context.Context, channel, resource string) error {
	return apiErr(a.cal.Channels.Stop(&calendar.Channel{Id: channel, ResourceId: resource}).Context(ctx).Do())
}

func (a *api) Tasks(ctx context.Context, list string, since *time.Time) ([]Task, error) {
	call := a.tasks.Tasks.List(list).MaxResults(100).ShowDeleted(true).ShowHidden(true).ShowCompleted(true)
	if since != nil {
		call = call.UpdatedMin(since.UTC().Format(time.RFC3339))
	}
	var out []Task
	err := call.Pages(ctx, func(p *tasks.Tasks) error {
		for _, t := range p.Items {
			out = append(out, fromTask(t))
		}
		return nil
	})
	return out, apiErr(err)
}

func (a *api) Task(ctx context.Context, list, id string) (Task, error) {
	t, err := a.tasks.Tasks.Get(list, id).Context(ctx).Do()
	if err != nil {
		return Task{}, apiErr(err)
	}
	return fromTask(t), nil
}

func (a *api) InsertTask(ctx context.Context, list string, t Task) (Task, error) {
	call := a.tasks.Tasks.Insert(list, toTask(t))
	if t.Parent != "" {
		call = call.Parent(t.Parent)
	}
	out, err := call.Context(ctx).Do()
	if err != nil {
		return Task{}, apiErr(err)
	}
	return fromTask(out), nil
}

func (a *api) PatchTask(ctx context.Context, list string, t Task) (Task, error) {
	out, err := a.tasks.Tasks.Patch(list, t.ID, toTask(t)).Context(ctx).Do()
	if err != nil {
		return Task{}, apiErr(err)
	}
	return fromTask(out), nil
}

func (a *api) MoveTask(ctx context.Context, list, id, parent string) (Task, error) {
	call := a.tasks.Tasks.Move(list, id)
	if parent != "" {
		call = call.Parent(parent)
	}
	out, err := call.Context(ctx).Do()
	if err != nil {
		return Task{}, apiErr(err)
	}
	return fromTask(out), nil
}

func (a *api) DeleteTask(ctx context.Context, list, id string) error {
	return apiErr(a.tasks.Tasks.Delete(list, id).Context(ctx).Do())
}

func fromWhen(w *calendar.EventDateTime) When {
	if w == nil {
		return When{}
	}
	return When{Date: w.Date, DateTime: w.DateTime, TimeZone: w.TimeZone}
}

func toWhen(w When) *calendar.EventDateTime {
	if w.Date != "" {
		return &calendar.EventDateTime{Date: w.Date, NullFields: []string{"DateTime", "TimeZone"}}
	}
	return &calendar.EventDateTime{DateTime: w.DateTime, TimeZone: w.TimeZone, NullFields: []string{"Date"}}
}

func fromEvent(e *calendar.Event, cal string) Event {
	out := Event{
		ID:               e.Id,
		ICalUID:          e.ICalUID,
		ETag:             e.Etag,
		Status:           e.Status,
		Summary:          e.Summary,
		Description:      e.Description,
		Location:         e.Location,
		Start:            fromWhen(e.Start),
		End:              fromWhen(e.End),
		Recurrence:       e.Recurrence,
		RecurringEventID: e.RecurringEventId,
		OriginalStart:    fromWhen(e.OriginalStartTime),
		Transparency:     e.Transparency,
		Visibility:       e.Visibility,
		Editable:         e.Organizer == nil || e.Organizer.Self || strings.EqualFold(e.Organizer.Email, cal),
	}
	out.UseDefaultReminders = e.Reminders == nil || e.Reminders.UseDefault
	if e.Reminders != nil {
		for _, r := range e.Reminders.Overrides {
			out.Reminders = append(out.Reminders, int(r.Minutes))
		}
	}
	out.Updated, _ = time.Parse(time.RFC3339, e.Updated)
	return out
}

func toEvent(e Event) *calendar.Event {
	out := &calendar.Event{
		Summary:         e.Summary,
		Description:     e.Description,
		Location:        e.Location,
		Status:          e.Status,
		Transparency:    e.Transparency,
		Visibility:      e.Visibility,
		Start:           toWhen(e.Start),
		End:             toWhen(e.End),
		ForceSendFields: []string{"Summary", "Description", "Location"},
	}
	rem := &calendar.EventReminders{UseDefault: false, Overrides: []*calendar.EventReminder{}, ForceSendFields: []string{"UseDefault", "Overrides"}}
	for _, m := range e.Reminders {
		rem.Overrides = append(rem.Overrides, &calendar.EventReminder{Method: "popup", Minutes: int64(m), ForceSendFields: []string{"Minutes"}})
	}
	out.Reminders = rem
	if e.SendRecurrence {
		out.Recurrence = e.Recurrence
		if out.Recurrence == nil {
			out.Recurrence = []string{}
		}
		out.ForceSendFields = append(out.ForceSendFields, "Recurrence")
	}
	return out
}

func fromTask(t *tasks.Task) Task {
	out := Task{ID: t.Id, ETag: t.Etag, Title: t.Title, Notes: t.Notes, Status: t.Status, Parent: t.Parent, Deleted: t.Deleted}
	if len(t.Due) >= 10 {
		out.Due = t.Due[:10]
	}
	if t.Completed != nil {
		if c, err := time.Parse(time.RFC3339, *t.Completed); err == nil {
			out.Completed = &c
		}
	}
	out.Updated, _ = time.Parse(time.RFC3339, t.Updated)
	return out
}

func toTask(t Task) *tasks.Task {
	out := &tasks.Task{Title: t.Title, Notes: t.Notes, Status: t.Status, ForceSendFields: []string{"Title", "Notes", "Status"}}
	if t.Due != "" {
		out.Due = t.Due + "T00:00:00.000Z"
	} else {
		out.NullFields = append(out.NullFields, "Due")
	}
	switch {
	case t.Status == TaskCompleted && t.Completed != nil:
		s := t.Completed.UTC().Format(time.RFC3339)
		out.Completed = &s
	case t.Status != TaskCompleted:
		out.NullFields = append(out.NullFields, "Completed")
	}
	return out
}
