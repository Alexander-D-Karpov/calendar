package todos

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/quickadd"
	"github.com/Alexander-D-Karpov/calendar/internal/service"
	"github.com/Alexander-D-Karpov/calendar/internal/store"
	"github.com/Alexander-D-Karpov/calendar/internal/undo"
	"github.com/Alexander-D-Karpov/calendar/internal/view"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

type Store interface {
	Restore(ctx context.Context, owner, id domain.ID, entity string) error
}

type Deps struct {
	Site  *web.Server
	Lists *service.TodoLists
	Todos *service.Todos
	Store Store
	Undo  *undo.Service
}

type Module struct {
	site  *web.Server
	lists *service.TodoLists
	todos *service.Todos
	store Store
	undo  *undo.Service
}

func New(d Deps) *Module {
	return &Module{site: d.Site, lists: d.Lists, todos: d.Todos, store: d.Store, undo: d.Undo}
}

func (m *Module) Routes(mux *http.ServeMux) {
	h, user := m.site.Handle, m.site.RequireUser
	mux.Handle("GET /todos", h(m.index, user))
	mux.Handle("GET /todos/new", h(m.newTodo, user))
	mux.Handle("POST /todos", h(m.create, user))
	mux.Handle("POST /todos/lists", h(m.createList, user))
	mux.Handle("GET /todos/{id}", h(m.show, user))
	mux.Handle("GET /todos/{id}/edit", h(m.edit, user))
	mux.Handle("POST /todos/{id}/edit", h(m.update, user))
	mux.Handle("POST /todos/{id}/toggle", h(m.toggle, user))
	mux.Handle("POST /todos/{id}/move-time", h(m.moveTime, user))
	mux.Handle("POST /todos/{id}/delete", h(m.remove, user))
	mux.Handle("POST /todos/{id}/checks", h(m.addCheck, user))
	mux.Handle("POST /todos/{id}/checks/{cid}/toggle", h(m.toggleCheck, user))
	mux.Handle("POST /todos/{id}/checks/{cid}/delete", h(m.deleteCheck, user))
}

func (m *Module) index(w http.ResponseWriter, r *http.Request) {
	v, lists, ok := m.load(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	name := q.Get("filter")
	var cur *domain.TodoList
	if raw := q.Get("list"); raw != "" {
		if cur = findList(lists, raw); cur == nil {
			m.site.Fail(w, r, domain.ErrNotFound)
			return
		}
	} else if web.LabelOf(filterOptions, name) == "" {
		cur = defaultList(lists)
	}
	f, iv := selection(v.Today(), cur, name)
	iv.Back = r.URL.RequestURI()
	todos, err := m.todos.Find(r.Context(), v.User.ID, f)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	iv.Rows = buildRows(todos, rowContext{
		lists: listMap(lists), today: v.Today(), clock24: v.Clock24(), showList: cur == nil, csrf: v.CSRF, back: iv.Back,
	})
	for _, o := range filterOptions {
		iv.Filters = append(iv.Filters, link{Label: o.Label, Href: "/todos?filter=" + o.Value, Current: cur == nil && name == o.Value})
	}
	for _, l := range lists {
		iv.Lists = append(iv.Lists, link{Label: l.Name, Href: "/todos?list=" + l.ID.String(), Hex: view.Hex(l.Color), Current: cur != nil && cur.ID == l.ID})
	}
	iv.Colors = listColors(lists)
	m.site.Render(w, r, http.StatusOK, "todos", v.Page("todos", iv.Heading, iv))
}

func (m *Module) show(w http.ResponseWriter, r *http.Request) {
	v, t, ok := m.loadTodo(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	l, err := m.lists.Get(ctx, v.User.ID, t.ListID)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	self := "/todos/" + t.ID.String()
	dv := detailView{
		Todo:      t,
		List:      l,
		Due:       dueLabel(t, v.Today(), v.Clock24()),
		Overdue:   overdue(t, v.Today()),
		Priority:  priorityLabel(t.Priority),
		Progress:  progress(t),
		Reminders: web.ReminderLabel(t.Reminders),
		Back:      "/todos?list=" + l.ID.String(),
		Self:      self,
		Hex:       view.Hex(l.Color),
		Colors:    []string{view.Hex(l.Color)},
	}
	if t.ParentID != nil {
		if p, err := m.todos.Get(ctx, v.User.ID, *t.ParentID); err == nil {
			dv.Parent = &p
		}
	} else {
		subs, err := m.todos.Find(ctx, v.User.ID, store.TodoFilter{Parent: &t.ID, Order: store.OrderByPosition})
		if err != nil {
			m.site.Fail(w, r, err)
			return
		}
		dv.Subtasks = buildRows(subs, rowContext{
			lists: map[domain.ID]domain.TodoList{l.ID: l}, today: v.Today(), clock24: v.Clock24(), csrf: v.CSRF, back: self,
		})
	}
	m.site.Render(w, r, http.StatusOK, "todo", v.Page("todos", t.Title, dv))
}

func (m *Module) newTodo(w http.ResponseWriter, r *http.Request) {
	v, lists, ok := m.load(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	due := q.Get("date")
	if due == "" {
		due = v.Today().Format(domain.DateLayout)
	}
	vals := url.Values{
		"timezone": {v.Loc.String()},
		"repeat":   {"none"},
		"priority": {"0"},
		"show":     {"auto"},
		"due_date": {due},
		"due_time": {q.Get("time")},
		"duration": {"30"},
	}
	fv := formView{Action: "/todos", Cancel: "/todos", AllowList: true}
	if l := defaultList(lists); l != nil {
		vals.Set("list_id", l.ID.String())
	}
	if l := findList(lists, q.Get("list")); l != nil {
		vals.Set("list_id", l.ID.String())
		fv.Cancel = "/todos?list=" + l.ID.String()
	}
	if id, err := domain.ParseID(q.Get("parent")); err == nil {
		p, err := m.todos.Get(r.Context(), v.User.ID, id)
		if err != nil {
			m.site.Fail(w, r, err)
			return
		}
		vals.Set("parent_id", p.ID.String())
		vals.Set("list_id", p.ListID.String())
		fv.AllowList, fv.ParentTitle, fv.Cancel = false, p.Title, "/todos/"+p.ID.String()
	}
	if web.IsFragment(r) {
		fv.Back = m.site.BackTo(r, "")
		fv.Cancel = fv.Back
	}
	m.renderForm(w, r, v, lists, http.StatusOK, web.NewForm(vals), fv, "New todo")
}

func (m *Module) create(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	f, ok := m.site.ParseForm(w, r)
	if !ok {
		return
	}
	if f.Get("quick") == "1" {
		parsed := quickadd.Parse(f.Get("title"), quickadd.Options{Now: v.Now, Loc: v.Loc, WeekStart: time.Weekday(v.User.WeekStart)})
		if parsed.Matched {
			var lists []domain.TodoList
			if parsed.List != "" {
				var lerr error
				if lists, lerr = m.lists.List(r.Context(), v.User.ID); lerr != nil {
					m.site.Fail(w, r, lerr)
					return
				}
			}
			f = f.Merge(quickValues(parsed, lists))
		}
	}
	full := f.Get("form") == "full"
	p, err := todoPatch(f, true)
	var t domain.Todo
	if err == nil {
		t, err = m.todos.Create(r.Context(), v.User.ID, p)
	}
	if err != nil && full {
		lists, lerr := m.lists.List(r.Context(), v.User.ID)
		if lerr != nil {
			m.site.Fail(w, r, lerr)
			return
		}
		back := f.Get("back")
		fv := formView{Action: "/todos", Cancel: web.SafeNext(back), Back: back, AllowList: f.Get("parent_id") == ""}
		m.site.Retry(w, r, err, f, func(status int) { m.renderForm(w, r, v, lists, status, f, fv, "New todo") })
		return
	}
	target := m.site.BackTo(r, f.Get("back"))
	if err == nil && full && !web.IsFragment(r) {
		target = "/todos/" + t.ID.String()
	}
	token := ""
	if err == nil {
		token = m.undo.Record(r.Context(), v.User.ID, domain.UndoTodoCreate, "Todo added.", todoRef{ID: t.ID.String()})
	}
	m.afterUndo(w, r, err, target, "Todo added.", token)
}

func (m *Module) edit(w http.ResponseWriter, r *http.Request) {
	v, t, ok := m.loadTodo(w, r)
	if !ok {
		return
	}
	lists, err := m.lists.List(r.Context(), v.User.ID)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	self := "/todos/" + t.ID.String()
	fv := formView{Action: self + "/edit", Cancel: self, ETag: t.ETag(), AllowList: t.ParentID == nil}
	if web.IsFragment(r) {
		fv.Back = m.site.BackTo(r, "")
		fv.Cancel = fv.Back
	}
	m.renderForm(w, r, v, lists, http.StatusOK, web.NewForm(formValues(t, v.Loc.String())), fv, "Edit todo")
}

func (m *Module) update(w http.ResponseWriter, r *http.Request) {
	v, t, ok := m.loadTodo(w, r)
	if !ok {
		return
	}
	f, ok := m.site.ParseForm(w, r)
	if !ok {
		return
	}
	self := "/todos/" + t.ID.String()
	p, err := todoPatch(f, t.ParentID == nil)
	if err == nil {
		_, err = m.todos.Update(r.Context(), v.User.ID, t.ID, p, f.Get("etag"))
	}
	if err != nil {
		lists, lerr := m.lists.List(r.Context(), v.User.ID)
		if lerr != nil {
			m.site.Fail(w, r, lerr)
			return
		}
		back := f.Get("back")
		fv := formView{Action: self + "/edit", Cancel: self, Back: back, ETag: f.Get("etag"), AllowList: t.ParentID == nil}
		m.site.Retry(w, r, err, f, func(status int) { m.renderForm(w, r, v, lists, status, f, fv, "Edit todo") })
		return
	}
	target := self
	if web.IsFragment(r) {
		target = m.site.BackTo(r, f.Get("back"))
	}
	token := m.undo.Record(r.Context(), v.User.ID, domain.UndoTodoRestore, "Todo saved.",
		todoSnapshot{ID: t.ID.String(), Values: formValues(t, v.Loc.String()), List: t.ParentID == nil})
	m.site.FlashUndo(w, r, "ok", "Todo saved.", token)
	m.site.Finish(w, r, target)
}

func (m *Module) toggle(w http.ResponseWriter, r *http.Request) {
	v, t, ok := m.loadTodo(w, r)
	if !ok {
		return
	}
	f, ok := m.site.ParseForm(w, r)
	if !ok {
		return
	}
	target := m.site.BackTo(r, f.Get("back"))
	was := t.Done()
	var err error
	msg := ""
	if was {
		_, err = m.todos.Reopen(r.Context(), v.User.ID, t.ID, "")
	} else {
		var next *domain.Todo
		_, next, err = m.todos.Complete(r.Context(), v.User.ID, t.ID, "")
		if next != nil && next.DueDate != nil {
			msg = "Done. The next one is due " + next.DueDate.Format("Mon 2 Jan") + "."
		}
	}
	token := ""
	if err == nil {
		token = m.undo.Record(r.Context(), v.User.ID, domain.UndoTodoStatus, "", todoStatus{ID: t.ID.String(), Done: was})
		if msg == "" {
			msg = "Todo reopened."
			if !was {
				msg = "Todo completed."
			}
		}
	}
	m.afterUndo(w, r, err, target, msg, token)
}

func (m *Module) remove(w http.ResponseWriter, r *http.Request) {
	v, t, ok := m.loadTodo(w, r)
	if !ok {
		return
	}
	f, ok := m.site.ParseForm(w, r)
	if !ok {
		return
	}
	target := "/todos?list=" + t.ListID.String()
	if b := f.Get("back"); b != "" {
		target = web.SafeNext(b)
	}
	err := m.todos.Delete(r.Context(), v.User.ID, t.ID, f.Get("etag"))
	token := ""
	if err == nil {
		token = m.undo.Record(r.Context(), v.User.ID, domain.UndoTodoDelete, "Todo deleted.", todoRef{ID: t.ID.String()})
	}
	m.afterUndo(w, r, err, target, "Todo deleted.", token)
}

func (m *Module) addCheck(w http.ResponseWriter, r *http.Request) {
	v, t, ok := m.loadTodo(w, r)
	if !ok {
		return
	}
	f, ok := m.site.ParseForm(w, r)
	if !ok {
		return
	}
	_, err := m.todos.AddCheck(r.Context(), v.User.ID, t.ID, domain.CheckPatch{Text: domain.Some(f.Get("text"))})
	m.after(w, r, err, "/todos/"+t.ID.String(), "")
}

func (m *Module) toggleCheck(w http.ResponseWriter, r *http.Request) {
	v, t, ok := m.loadTodo(w, r)
	if !ok {
		return
	}
	cid, err := domain.ParseID(r.PathValue("cid"))
	var cur *domain.Check
	for i := range t.Checks {
		if t.Checks[i].ID == cid {
			cur = &t.Checks[i]
		}
	}
	if err != nil || cur == nil {
		m.site.Fail(w, r, domain.ErrNotFound)
		return
	}
	_, err = m.todos.UpdateCheck(r.Context(), v.User.ID, t.ID, cid, domain.CheckPatch{Done: domain.Some(!cur.Done)})
	m.after(w, r, err, "/todos/"+t.ID.String(), "")
}

func (m *Module) deleteCheck(w http.ResponseWriter, r *http.Request) {
	v, t, ok := m.loadTodo(w, r)
	if !ok {
		return
	}
	cid, err := domain.ParseID(r.PathValue("cid"))
	if err != nil {
		m.site.Fail(w, r, domain.ErrNotFound)
		return
	}
	m.after(w, r, m.todos.DeleteCheck(r.Context(), v.User.ID, t.ID, cid), "/todos/"+t.ID.String(), "")
}

func (m *Module) createList(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	f, ok := m.site.ParseForm(w, r)
	if !ok {
		return
	}
	l, err := m.lists.Create(r.Context(), v.User.ID, domain.TodoListPatch{Name: domain.Some(f.Get("name"))})
	target := "/todos"
	if err == nil {
		target += "?list=" + l.ID.String()
	}
	m.after(w, r, err, target, "List created.")
}

func (m *Module) load(w http.ResponseWriter, r *http.Request) (web.Viewer, []domain.TodoList, bool) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return web.Viewer{}, nil, false
	}
	lists, err := m.lists.List(r.Context(), v.User.ID)
	if err != nil {
		m.site.Fail(w, r, err)
		return web.Viewer{}, nil, false
	}
	return v, lists, true
}

func (m *Module) loadTodo(w http.ResponseWriter, r *http.Request) (web.Viewer, domain.Todo, bool) {
	id, err := domain.ParseID(r.PathValue("id"))
	if err != nil {
		m.site.Fail(w, r, domain.ErrNotFound)
		return web.Viewer{}, domain.Todo{}, false
	}
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return web.Viewer{}, domain.Todo{}, false
	}
	t, err := m.todos.Get(r.Context(), v.User.ID, id)
	if err != nil {
		m.site.Fail(w, r, err)
		return web.Viewer{}, domain.Todo{}, false
	}
	return v, t, true
}

func (m *Module) renderForm(w http.ResponseWriter, r *http.Request, v web.Viewer, lists []domain.TodoList, status int, f *web.Form, fv formView, title string) {
	fv.Lists, fv.Repeats, fv.Priorities, fv.Shows = lists, web.RepeatOptions, priorityOptions, showOptions
	p := v.Page("todos", title, fv)
	p.Form = f
	m.site.RenderBlock(w, r, status, "todo_edit", web.Block(r, "todo_form"), p)
}

func (m *Module) after(w http.ResponseWriter, r *http.Request, err error, target, ok string) {
	m.afterUndo(w, r, err, target, ok, "")
}

func (m *Module) afterUndo(w http.ResponseWriter, r *http.Request, err error, target, ok, token string) {
	if err != nil {
		msg := web.ErrorText(err)
		if msg == "" {
			m.site.Fail(w, r, err)
			return
		}
		m.site.Flash(w, r, "error", msg)
	} else if ok != "" {
		m.site.FlashUndo(w, r, "ok", ok, token)
	}
	m.site.Finish(w, r, target)
}

func quickValues(p quickadd.Result, lists []domain.TodoList) url.Values {
	v := url.Values{}
	v.Set("title", p.Title)
	if p.Date != nil {
		v.Set("due_date", p.Date.Format(domain.DateLayout))
	}
	if p.Time != nil {
		v.Set("due_time", p.Time.String())
		v.Set("show_on_calendar", "1")
	}
	switch {
	case p.Duration > 0:
		v.Set("duration_min", strconv.Itoa(p.Duration))
	case p.Time != nil && p.End != nil:
		if d := p.End.Minutes() - p.Time.Minutes(); d > 0 {
			v.Set("duration_min", strconv.Itoa(d))
		}
	}
	if p.Priority > 0 {
		v.Set("priority", strconv.Itoa(p.Priority))
	}
	if p.List != "" {
		for _, l := range lists {
			if strings.EqualFold(strings.ReplaceAll(l.Name, " ", "-"), p.List) {
				v.Set("list_id", l.ID.String())
				break
			}
		}
	}
	return v
}

// moveTime reschedules a todo dragged around the calendar. An all-day drop
// sends only a date and clears the time, so dropping onto the all-day lane
// turns a timed todo back into a dated one.
func (m *Module) moveTime(w http.ResponseWriter, r *http.Request) {
	v, t, ok := m.loadTodo(w, r)
	if !ok {
		return
	}
	f, ok := m.site.ParseForm(w, r)
	if !ok {
		return
	}
	p, err := movePatch(f, v.Loc)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	before := formValues(t, v.Loc.String())
	if _, err = m.todos.Update(r.Context(), v.User.ID, t.ID, p, ""); err != nil {
		m.afterUndo(w, r, err, m.site.BackTo(r, ""), "", "")
		return
	}
	token := m.undo.Record(r.Context(), v.User.ID, domain.UndoTodoRestore, "Todo moved.",
		todoSnapshot{ID: t.ID.String(), Values: before, List: t.ParentID == nil})
	m.afterUndo(w, r, nil, m.site.BackTo(r, ""), "Todo moved.", token)
}

func movePatch(f *web.Form, loc *time.Location) (domain.TodoPatch, error) {
	var p domain.TodoPatch
	if date := f.Get("date"); date != "" {
		day, err := time.ParseInLocation(domain.DateLayout, date, loc)
		if err != nil {
			return p, fmt.Errorf("%w: date must be a local date", domain.ErrInvalid)
		}
		p.DueDate = domain.Some(day.Format(domain.DateLayout))
		p.DueTime = domain.Opt[string]{Set: true, Null: true}
		p.DurationMin = domain.Opt[int]{Set: true, Null: true}
		return p, nil
	}
	start, err := time.ParseInLocation("2006-01-02T15:04", f.Get("start"), loc)
	if err != nil {
		return p, fmt.Errorf("%w: start must be a local date and time", domain.ErrInvalid)
	}
	p.DueDate = domain.Some(start.Format(domain.DateLayout))
	p.DueTime = domain.Some(start.Format(domain.TimeLayout))
	p.Timezone = domain.Some(loc.String())
	p.ShowOnCalendar = domain.Some(true)
	if raw := f.Get("end"); raw != "" {
		end, err := time.ParseInLocation("2006-01-02T15:04", raw, loc)
		if err != nil {
			return p, fmt.Errorf("%w: end must be a local date and time", domain.ErrInvalid)
		}
		if mins := int(end.Sub(start) / time.Minute); mins > 0 {
			p.DurationMin = domain.Some(mins)
		}
	}
	return p, nil
}
