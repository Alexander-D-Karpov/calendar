package api

import (
	"net/http"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/service"
)

type checkJSON struct {
	ID       string     `json:"id"`
	Text     string     `json:"text"`
	Done     bool       `json:"done"`
	Position string     `json:"position"`
	DoneAt   *time.Time `json:"done_at"`
}

type checkList struct {
	Items []checkJSON `json:"items"`
}

type checkOrder struct {
	IDs []string `json:"ids"`
}

type todoJSON struct {
	ID             string      `json:"id"`
	ListID         string      `json:"list_id"`
	ParentID       *string     `json:"parent_id"`
	Title          string      `json:"title"`
	Body           string      `json:"body"`
	Status         string      `json:"status"`
	Priority       int         `json:"priority"`
	Position       string      `json:"position"`
	DueDate        *string     `json:"due_date"`
	DueTime        *string     `json:"due_time"`
	DurationMin    *int        `json:"duration_min"`
	Timezone       *string     `json:"timezone"`
	RRule          *string     `json:"rrule"`
	ShowOnCalendar bool        `json:"show_on_calendar"`
	CompletedAt    *time.Time  `json:"completed_at"`
	Reminders      []int       `json:"reminders"`
	Checks         []checkJSON `json:"checks"`
	Version        int64       `json:"version"`
	CreatedAt      time.Time   `json:"created_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
}

type todoPage struct {
	Items      []todoJSON `json:"items"`
	NextCursor *string    `json:"next_cursor"`
}

type completionJSON struct {
	Todo   todoJSON `json:"todo"`
	NextID *string  `json:"next_id"`
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func toCheckJSON(c domain.Check) checkJSON {
	return checkJSON{ID: c.ID.String(), Text: c.Text, Done: c.Done, Position: c.Position, DoneAt: utcPtr(c.DoneAt)}
}

func toCheckList(cs []domain.Check) checkList {
	items := make([]checkJSON, len(cs))
	for i, c := range cs {
		items[i] = toCheckJSON(c)
	}
	return checkList{Items: items}
}

func toTodoJSON(t domain.Todo) todoJSON {
	out := todoJSON{
		ID:             t.ID.String(),
		ListID:         t.ListID.String(),
		Title:          t.Title,
		Body:           t.Body,
		Status:         t.Status,
		Priority:       t.Priority,
		Position:       t.Position,
		Timezone:       optString(t.TZ),
		RRule:          optString(t.RRule),
		ShowOnCalendar: t.ShowOnCalendar,
		CompletedAt:    utcPtr(t.CompletedAt),
		Reminders:      t.Reminders,
		Checks:         toCheckList(t.Checks).Items,
		Version:        t.Version,
		CreatedAt:      t.CreatedAt.UTC(),
		UpdatedAt:      t.UpdatedAt.UTC(),
	}
	if t.ParentID != nil {
		out.ParentID = optString(t.ParentID.String())
	}
	if t.DueDate != nil {
		out.DueDate = optString(t.DueDate.Format(domain.DateLayout))
	}
	if t.DueTime != nil {
		out.DueTime = optString(domain.FormatClock(*t.DueTime))
	}
	if t.Duration > 0 {
		d := t.Duration
		out.DurationMin = &d
	}
	if out.Reminders == nil {
		out.Reminders = []int{}
	}
	return out
}

func toTodoPage(list []domain.Todo, next string) todoPage {
	page := todoPage{Items: make([]todoJSON, len(list)), NextCursor: optString(next)}
	for i, t := range list {
		page.Items[i] = toTodoJSON(t)
	}
	return page
}

func toCompletion(t domain.Todo, next *domain.Todo) completionJSON {
	out := completionJSON{Todo: toTodoJSON(t)}
	if next != nil {
		out.NextID = optString(next.ID.String())
	}
	return out
}

func listTodos(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		q := r.URL.Query()
		limit, err := queryInt(q, "limit", 0)
		if err != nil {
			return err
		}
		list, next, err := d.Todos.List(r.Context(), ownerID(r), service.TodoQuery{
			Lists:   queryList(q, "list_id"),
			Parent:  q.Get("parent_id"),
			Status:  q.Get("status"),
			DueFrom: q.Get("due_from"),
			DueTo:   q.Get("due_to"),
			Cursor:  q.Get("cursor"),
			Limit:   limit,
		})
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, toTodoPage(list, next))
		return nil
	}
}

func getTodo(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		t, err := d.Todos.Get(r.Context(), ownerID(r), id)
		if err != nil {
			return err
		}
		writeResource(w, r, http.StatusOK, t.ETag(), toTodoJSON(t))
		return nil
	}
}

func createTodo(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		var p domain.TodoPatch
		if err := decodeJSON(r, &p); err != nil {
			return err
		}
		t, err := d.Todos.Create(r.Context(), ownerID(r), p)
		if err != nil {
			return err
		}
		w.Header().Set("Location", Prefix+"/todos/"+t.ID.String())
		writeResource(w, r, http.StatusCreated, t.ETag(), toTodoJSON(t))
		return nil
	}
}

func updateTodo(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		var p domain.TodoPatch
		if err := decodeJSON(r, &p, mediaMergePatch, mediaJSON); err != nil {
			return err
		}
		t, err := d.Todos.Update(r.Context(), ownerID(r), id, p, r.Header.Get("If-Match"))
		if err != nil {
			return err
		}
		writeResource(w, r, http.StatusOK, t.ETag(), toTodoJSON(t))
		return nil
	}
}

func deleteTodo(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		if err := d.Todos.Delete(r.Context(), ownerID(r), id, r.Header.Get("If-Match")); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}

func completeTodo(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		t, next, err := d.Todos.Complete(r.Context(), ownerID(r), id, r.Header.Get("If-Match"))
		if err != nil {
			return err
		}
		writeResource(w, r, http.StatusOK, t.ETag(), toCompletion(t, next))
		return nil
	}
}

func reopenTodo(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		t, err := d.Todos.Reopen(r.Context(), ownerID(r), id, r.Header.Get("If-Match"))
		if err != nil {
			return err
		}
		writeResource(w, r, http.StatusOK, t.ETag(), toTodoJSON(t))
		return nil
	}
}

func moveTodo(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		var m domain.TodoMove
		if err := decodeJSON(r, &m); err != nil {
			return err
		}
		t, err := d.Todos.Move(r.Context(), ownerID(r), id, m, r.Header.Get("If-Match"))
		if err != nil {
			return err
		}
		writeResource(w, r, http.StatusOK, t.ETag(), toTodoJSON(t))
		return nil
	}
}

func listChecks(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		cs, err := d.Todos.Checks(r.Context(), ownerID(r), id)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, toCheckList(cs))
		return nil
	}
}

func createCheck(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		var p domain.CheckPatch
		if err := decodeJSON(r, &p); err != nil {
			return err
		}
		c, err := d.Todos.AddCheck(r.Context(), ownerID(r), id, p)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusCreated, toCheckJSON(c))
		return nil
	}
}

func updateCheck(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		cid, err := pathParam(r, "cid")
		if err != nil {
			return err
		}
		var p domain.CheckPatch
		if err := decodeJSON(r, &p, mediaMergePatch, mediaJSON); err != nil {
			return err
		}
		c, err := d.Todos.UpdateCheck(r.Context(), ownerID(r), id, cid, p)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, toCheckJSON(c))
		return nil
	}
}

func deleteCheck(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		cid, err := pathParam(r, "cid")
		if err != nil {
			return err
		}
		if err := d.Todos.DeleteCheck(r.Context(), ownerID(r), id, cid); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}

func reorderChecks(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		var o checkOrder
		if err := decodeJSON(r, &o); err != nil {
			return err
		}
		cs, err := d.Todos.ReorderChecks(r.Context(), ownerID(r), id, o.IDs)
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, toCheckList(cs))
		return nil
	}
}
