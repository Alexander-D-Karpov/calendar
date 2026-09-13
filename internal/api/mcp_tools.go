package api

import (
	"context"
	"encoding/json"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/search"
	"github.com/Alexander-D-Karpov/calendar/internal/service"
)

var (
	readHints   = toolHints{ReadOnly: true, Idempotent: true}
	createHints = toolHints{}
	updateHints = toolHints{Idempotent: true}
	deleteHints = toolHints{Destructive: true, Idempotent: true}
)

type eventArgs struct {
	ID       string `json:"id"`
	Scope    string `json:"scope"`
	Instance string `json:"instance"`
}

func object(props map[string]any, required ...string) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	o := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}

func field(typ, desc string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}

func enumField(desc string, values ...string) map[string]any {
	f := field("string", desc)
	f["enum"] = values
	return f
}

func idField(desc string) map[string]any {
	f := field("string", desc)
	f["format"] = "uuid"
	return f
}

func idList(desc string) map[string]any {
	return map[string]any{"type": "array", "description": desc, "items": map[string]any{"type": "string", "format": "uuid"}}
}

func idOf(raw json.RawMessage) (domain.ID, error) {
	var a struct {
		ID string `json:"id"`
	}
	if err := decodeArgs(raw, &a); err != nil {
		return domain.NilID, err
	}
	return argID("id", a.ID)
}

func mcpTools(d Deps, s schemaSet) []mcpTool {
	eventRef := map[string]any{
		"id":       idField("Event ID. Occurrences of a series share the series ID."),
		"scope":    enumField("Part of a recurring series. Defaults to this when instance is set and to all otherwise.", domain.ScopeThis, domain.ScopeFollowing, domain.ScopeAll),
		"instance": field("string", "The instance value of an occurrence as returned by list_events. Required with this and following on a series."),
	}
	todoRef := map[string]any{"id": idField("Todo ID.")}
	limit := field("integer", "Items per page, 1 to 500. Defaults to 100.")
	limit["minimum"], limit["maximum"] = 1, 500

	return []mcpTool{
		{
			Name: "get_account", Title: "Get account", Annotations: readHints, scope: auth.ScopeAccountRead,
			Description: "Returns the account with its IANA timezone, week start, clock and sleep schedule. Call it first so dates and times are read correctly.",
			InputSchema: object(nil),
			run: func(ctx context.Context, owner domain.ID, _ json.RawMessage) (any, error) {
				u, ws, err := d.Settings.Get(ctx, owner)
				if err != nil {
					return nil, err
				}
				return toMe(u, ws), nil
			},
		},
		{
			Name: "update_account", Title: "Update account", Annotations: updateHints, scope: auth.ScopeAccountWrite,
			Description: "Changes the display name, timezone, week start, clock, default view or sleep schedule. sleep replaces the whole schedule.",
			InputSchema: s.input("AccountInput", nil),
			run: func(ctx context.Context, owner domain.ID, raw json.RawMessage) (any, error) {
				var p domain.SettingsPatch
				if err := decodeArgs(raw, &p); err != nil {
					return nil, err
				}
				u, ws, err := d.Settings.Update(ctx, owner, p, "")
				if err != nil {
					return nil, err
				}
				return toMe(u, ws), nil
			},
		},
		{
			Name: "list_calendars", Title: "List calendars", Annotations: readHints, scope: auth.ScopeCalendarsRead,
			Description: "Returns every calendar with its ID, color, timezone and whether it is the default or read only.",
			InputSchema: object(nil),
			run: func(ctx context.Context, owner domain.ID, _ json.RawMessage) (any, error) {
				list, err := d.Calendars.List(ctx, owner)
				if err != nil {
					return nil, err
				}
				return toCalendarList(list), nil
			},
		},
		{
			Name: "export_calendar", Title: "Export calendars", Annotations: readHints, scope: auth.ScopeExportsRead,
			Description: "Returns calendars and their events as an iCalendar document. Without calendar_id every calendar is exported.",
			InputSchema: object(map[string]any{"calendar_id": idList("Only these calendars.")}),
			run: func(ctx context.Context, owner domain.ID, raw json.RawMessage) (any, error) {
				var a struct {
					CalendarID []string `json:"calendar_id"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return nil, err
				}
				ids := make([]domain.ID, 0, len(a.CalendarID))
				for _, s := range a.CalendarID {
					id, err := argID("calendar_id", s)
					if err != nil {
						return nil, err
					}
					ids = append(ids, id)
				}
				f, err := d.Export.ICS(ctx, owner, ids)
				if err != nil {
					return nil, err
				}
				return map[string]any{"filename": f.Name, "ics": string(f.Body)}, nil
			},
		},
		{
			Name: "list_subscriptions", Title: "List subscriptions", Annotations: readHints, scope: auth.ScopeCalendarsRead,
			Description: "Returns followed ICS feeds with their refresh status.",
			InputSchema: object(nil),
			run: func(ctx context.Context, owner domain.ID, _ json.RawMessage) (any, error) {
				rows, err := d.Subs.List(ctx, owner)
				if err != nil {
					return nil, err
				}
				items := make([]subscriptionJSON, len(rows))
				for i, s := range rows {
					items[i] = subscriptionJSON{
						ID: s.ID.String(), CalendarID: s.CalendarID.String(), URL: s.URL, RefreshEvery: s.Interval.String(),
						Status: s.Status, NextRefreshAt: s.NextRefreshAt.UTC(), LastOKAt: utcPtr(s.LastOKAt), LastError: s.LastError,
					}
				}
				return subscriptionList{Items: items}, nil
			},
		},
		{
			Name: "list_events", Title: "List events", Annotations: readHints, scope: auth.ScopeCalendarsRead,
			Description: "Returns events and occurrences overlapping the range, ordered by start. The range can be at most 400 days.",
			InputSchema: object(map[string]any{
				"from":        field("string", "Start of the range, inclusive: an RFC 3339 date-time, or a date for midnight in the account timezone."),
				"to":          field("string", "End of the range, exclusive, in the same format as from."),
				"calendar_id": idList("Only these calendars. Without it every calendar that is not hidden is included."),
				"expand":      field("boolean", "Expand recurring series into occurrences. Defaults to true."),
			}, "from", "to"),
			run: func(ctx context.Context, owner domain.ID, raw json.RawMessage) (any, error) {
				var a struct {
					From       string   `json:"from"`
					To         string   `json:"to"`
					CalendarID []string `json:"calendar_id"`
					Expand     *bool    `json:"expand"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return nil, err
				}
				list, err := d.Events.List(ctx, owner, service.EventQuery{
					From: a.From, To: a.To, Calendars: a.CalendarID, Expand: a.Expand == nil || *a.Expand,
				})
				if err != nil {
					return nil, err
				}
				return toEventList(list), nil
			},
		},
		{
			Name: "search", Title: "Search", Annotations: readHints, scope: auth.ScopeCalendarsRead,
			Description: "Finds events and todos by text. Supports \"exact phrase\", -exclude, and the filters in:<calendar or list>, type:event, type:todo, is:open, is:done, is:overdue, is:recurring, is:private, has:time, has:checks, has:body, has:location, has:reminders, after: and before: taking a date or today or +7d, and priority:>=2.",
			InputSchema: object(map[string]any{
				"q":      field("string", "The query, for example: standup in:Work after:today"),
				"type":   enumField("Limit to one kind. The type: filter in the query wins.", "event", "todo"),
				"limit":  field("integer", "Results per page, 1 to 100. Defaults to 25."),
				"offset": field("integer", "Results to skip, for paging."),
			}, "q"),
			run: func(ctx context.Context, owner domain.ID, raw json.RawMessage) (any, error) {
				var a struct {
					Q      string `json:"q"`
					Type   string `json:"type"`
					Limit  int    `json:"limit"`
					Offset int    `json:"offset"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return nil, err
				}
				res, err := d.Search.Search(ctx, owner, search.Request{Raw: a.Q, Type: a.Type, Limit: a.Limit, Offset: a.Offset})
				if err != nil {
					return nil, err
				}
				out := searchResponse{Query: res.Query.Raw, Type: res.Query.Type, Items: make([]hitJSON, len(res.Hits)), Warnings: res.Warnings}
				for i, h := range res.Hits {
					out.Items[i] = hitJSON{
						Kind: h.Kind, ID: h.ID.String(), Title: h.Title, Snippet: h.Snippet, Container: h.Container,
						AllDay: h.AllDay, Done: h.Done, Start: utcPtr(h.Start), End: utcPtr(h.End),
					}
				}
				if res.More {
					next := res.Next()
					out.NextOffset = &next
				}
				return out, nil
			},
		},
		{
			Name: "get_event", Title: "Get event", Annotations: readHints, scope: auth.ScopeCalendarsRead,
			Description: "Returns one stored event: a single event, a recurring series, or an occurrence changed on its own.",
			InputSchema: object(map[string]any{"id": idField("Event ID.")}, "id"),
			run: func(ctx context.Context, owner domain.ID, raw json.RawMessage) (any, error) {
				id, err := idOf(raw)
				if err != nil {
					return nil, err
				}
				e, err := d.Events.Get(ctx, owner, id)
				if err != nil {
					return nil, err
				}
				return eventResponse(e), nil
			},
		},
		{
			Name: "create_event", Title: "Create event", Annotations: createHints, scope: auth.ScopeCalendarsWrite,
			Description: "Creates an event. Only start is required. Without calendar_id it goes to the default calendar, without end a timed event lasts one hour and an all-day event one day.",
			InputSchema: s.input("EventInput", nil, "start"),
			run: func(ctx context.Context, owner domain.ID, raw json.RawMessage) (any, error) {
				var p domain.EventPatch
				if err := decodeArgs(raw, &p); err != nil {
					return nil, err
				}
				e, err := d.Events.Create(ctx, owner, p)
				if err != nil {
					return nil, err
				}
				return eventResponse(e), nil
			},
		},
		{
			Name: "update_event", Title: "Update event", Annotations: updateHints, scope: auth.ScopeCalendarsWrite,
			Description: "Changes only the fields given; null clears a nullable field. Changing start without end keeps the duration. For a series use scope and instance.",
			InputSchema: s.input("EventInput", eventRef, "id"),
			run: func(ctx context.Context, owner domain.ID, raw json.RawMessage) (any, error) {
				head, rest, err := splitArgs(raw, "id", "scope", "instance")
				if err != nil {
					return nil, err
				}
				var a eventArgs
				if err := decodeArgs(head, &a); err != nil {
					return nil, err
				}
				id, err := argID("id", a.ID)
				if err != nil {
					return nil, err
				}
				var p domain.EventPatch
				if err := decodeArgs(rest, &p); err != nil {
					return nil, err
				}
				e, err := d.Events.Update(ctx, owner, id, p, service.Edit{Scope: a.Scope, Instance: a.Instance})
				if err != nil {
					return nil, err
				}
				return eventResponse(e), nil
			},
		},
		{
			Name: "delete_event", Title: "Delete event", Annotations: deleteHints, scope: auth.ScopeCalendarsWrite,
			Description: "Moves an event to the trash. For a series, this excludes one occurrence, following ends the series there, and all deletes it.",
			InputSchema: object(eventRef, "id"),
			run: func(ctx context.Context, owner domain.ID, raw json.RawMessage) (any, error) {
				var a eventArgs
				if err := decodeArgs(raw, &a); err != nil {
					return nil, err
				}
				id, err := argID("id", a.ID)
				if err != nil {
					return nil, err
				}
				if err := d.Events.Delete(ctx, owner, id, service.Edit{Scope: a.Scope, Instance: a.Instance}); err != nil {
					return nil, err
				}
				return map[string]any{"deleted": true}, nil
			},
		},
		{
			Name: "list_todo_lists", Title: "List todo lists", Annotations: readHints, scope: auth.ScopeTodosRead,
			Description: "Returns every todo list with its ID and whether it is the default.",
			InputSchema: object(nil),
			run: func(ctx context.Context, owner domain.ID, _ json.RawMessage) (any, error) {
				list, err := d.TodoLists.List(ctx, owner)
				if err != nil {
					return nil, err
				}
				return toTodoListList(list), nil
			},
		},
		{
			Name: "list_todos", Title: "List todos", Annotations: readHints, scope: auth.ScopeTodosRead,
			Description: "Returns one page of todos with their checklists, open ones by default. Pass next_cursor as cursor for the next page.",
			InputSchema: object(map[string]any{
				"list_id":   idList("Only these lists."),
				"parent_id": idField("Only subtasks of this todo."),
				"status":    enumField("Which todos to include. Defaults to open.", "open", "completed", "all"),
				"due_from":  field("string", "Only todos due on or after this date, like 2026-09-10."),
				"due_to":    field("string", "Only todos due before this date."),
				"cursor":    field("string", "The next_cursor of the previous page."),
				"limit":     limit,
			}),
			run: func(ctx context.Context, owner domain.ID, raw json.RawMessage) (any, error) {
				var a struct {
					ListID   []string `json:"list_id"`
					ParentID string   `json:"parent_id"`
					Status   string   `json:"status"`
					DueFrom  string   `json:"due_from"`
					DueTo    string   `json:"due_to"`
					Cursor   string   `json:"cursor"`
					Limit    int      `json:"limit"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return nil, err
				}
				list, next, err := d.Todos.List(ctx, owner, service.TodoQuery{
					Lists: a.ListID, Parent: a.ParentID, Status: a.Status, DueFrom: a.DueFrom, DueTo: a.DueTo, Cursor: a.Cursor, Limit: a.Limit,
				})
				if err != nil {
					return nil, err
				}
				return toTodoPage(list, next), nil
			},
		},
		{
			Name: "get_todo", Title: "Get todo", Annotations: readHints, scope: auth.ScopeTodosRead,
			Description: "Returns one todo with its checklist.",
			InputSchema: object(todoRef, "id"),
			run: func(ctx context.Context, owner domain.ID, raw json.RawMessage) (any, error) {
				id, err := idOf(raw)
				if err != nil {
					return nil, err
				}
				t, err := d.Todos.Get(ctx, owner, id)
				if err != nil {
					return nil, err
				}
				return toTodoJSON(t), nil
			},
		},
		{
			Name: "create_todo", Title: "Create todo", Annotations: createHints, scope: auth.ScopeTodosWrite,
			Description: "Creates a todo. Only title is required. It goes to the default list unless list_id or parent_id is set. due_time needs due_date.",
			InputSchema: s.input("TodoInput", nil, "title"),
			run: func(ctx context.Context, owner domain.ID, raw json.RawMessage) (any, error) {
				var p domain.TodoPatch
				if err := decodeArgs(raw, &p); err != nil {
					return nil, err
				}
				t, err := d.Todos.Create(ctx, owner, p)
				if err != nil {
					return nil, err
				}
				return toTodoJSON(t), nil
			},
		},
		{
			Name: "update_todo", Title: "Update todo", Annotations: updateHints, scope: auth.ScopeTodosWrite,
			Description: "Changes only the fields given; null clears a nullable field. Clearing due_date also clears the time, duration and recurrence.",
			InputSchema: s.input("TodoInput", todoRef, "id"),
			run: func(ctx context.Context, owner domain.ID, raw json.RawMessage) (any, error) {
				head, rest, err := splitArgs(raw, "id")
				if err != nil {
					return nil, err
				}
				id, err := idOf(head)
				if err != nil {
					return nil, err
				}
				var p domain.TodoPatch
				if err := decodeArgs(rest, &p); err != nil {
					return nil, err
				}
				t, err := d.Todos.Update(ctx, owner, id, p, "")
				if err != nil {
					return nil, err
				}
				return toTodoJSON(t), nil
			},
		},
		{
			Name: "complete_todo", Title: "Complete todo", Annotations: updateHints, scope: auth.ScopeTodosWrite,
			Description: "Marks a todo completed. For a recurring todo the next occurrence is created and its ID returned in next_id.",
			InputSchema: object(todoRef, "id"),
			run: func(ctx context.Context, owner domain.ID, raw json.RawMessage) (any, error) {
				id, err := idOf(raw)
				if err != nil {
					return nil, err
				}
				t, next, err := d.Todos.Complete(ctx, owner, id, "")
				if err != nil {
					return nil, err
				}
				return toCompletion(t, next), nil
			},
		},
		{
			Name: "reopen_todo", Title: "Reopen todo", Annotations: updateHints, scope: auth.ScopeTodosWrite,
			Description: "Marks a completed todo as open again.",
			InputSchema: object(todoRef, "id"),
			run: func(ctx context.Context, owner domain.ID, raw json.RawMessage) (any, error) {
				id, err := idOf(raw)
				if err != nil {
					return nil, err
				}
				t, err := d.Todos.Reopen(ctx, owner, id, "")
				if err != nil {
					return nil, err
				}
				return toTodoJSON(t), nil
			},
		},
		{
			Name: "delete_todo", Title: "Delete todo", Annotations: deleteHints, scope: auth.ScopeTodosWrite,
			Description: "Moves a todo and its subtasks to the trash.",
			InputSchema: object(todoRef, "id"),
			run: func(ctx context.Context, owner domain.ID, raw json.RawMessage) (any, error) {
				id, err := idOf(raw)
				if err != nil {
					return nil, err
				}
				if err := d.Todos.Delete(ctx, owner, id, ""); err != nil {
					return nil, err
				}
				return map[string]any{"deleted": true}, nil
			},
		},
		{
			Name: "add_check", Title: "Add checklist item", Annotations: createHints, scope: auth.ScopeTodosWrite,
			Description: "Adds an item at the end of a todo's checklist.",
			InputSchema: object(map[string]any{"id": idField("Todo ID."), "text": field("string", "Item text, 1 to 500 characters.")}, "id", "text"),
			run: func(ctx context.Context, owner domain.ID, raw json.RawMessage) (any, error) {
				var a struct {
					ID   string `json:"id"`
					Text string `json:"text"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return nil, err
				}
				id, err := argID("id", a.ID)
				if err != nil {
					return nil, err
				}
				c, err := d.Todos.AddCheck(ctx, owner, id, domain.CheckPatch{Text: domain.Some(a.Text)})
				if err != nil {
					return nil, err
				}
				return toCheckJSON(c), nil
			},
		},
		{
			Name: "update_check", Title: "Update checklist item", Annotations: updateHints, scope: auth.ScopeTodosWrite,
			Description: "Changes the text of a checklist item or checks and unchecks it. Checking every item does not complete the todo.",
			InputSchema: object(map[string]any{
				"id":       idField("Todo ID."),
				"check_id": idField("Checklist item ID."),
				"text":     field("string", "New item text."),
				"done":     field("boolean", "Whether the item is checked."),
			}, "id", "check_id"),
			run: func(ctx context.Context, owner domain.ID, raw json.RawMessage) (any, error) {
				var a struct {
					ID      string  `json:"id"`
					CheckID string  `json:"check_id"`
					Text    *string `json:"text"`
					Done    *bool   `json:"done"`
				}
				if err := decodeArgs(raw, &a); err != nil {
					return nil, err
				}
				id, err := argID("id", a.ID)
				if err != nil {
					return nil, err
				}
				cid, err := argID("check_id", a.CheckID)
				if err != nil {
					return nil, err
				}
				var p domain.CheckPatch
				if a.Text != nil {
					p.Text = domain.Some(*a.Text)
				}
				if a.Done != nil {
					p.Done = domain.Some(*a.Done)
				}
				c, err := d.Todos.UpdateCheck(ctx, owner, id, cid, p)
				if err != nil {
					return nil, err
				}
				return toCheckJSON(c), nil
			},
		},
	}
}
