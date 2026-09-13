package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/recurrence"
	"github.com/Alexander-D-Karpov/calendar/internal/store"
)

const (
	defaultTodoLimit = 100
	maxTodoLimit     = 500
)

type TodoRepo interface {
	ListTodoLists(ctx context.Context, owner domain.ID) ([]domain.TodoList, error)
	Todo(ctx context.Context, owner, id domain.ID) (domain.Todo, error)
	Todos(ctx context.Context, owner domain.ID, f store.TodoFilter) ([]domain.Todo, error)
	WithTodos(ctx context.Context, owner domain.ID, fn func(store.TodoTx) error) error
}

type TodoQuery struct {
	Lists   []string
	Parent  string
	Status  string
	DueFrom string
	DueTo   string
	Cursor  string
	Limit   int
}

type Todos struct {
	repo  TodoRepo
	users UserRepo
	clock clock.Clock
}

func NewTodos(repo TodoRepo, users UserRepo, clk clock.Clock) *Todos {
	if clk == nil {
		clk = clock.New()
	}
	return &Todos{repo: repo, users: users, clock: clk}
}

func (s *Todos) now() time.Time {
	return s.clock.Now().UTC()
}

func (s *Todos) Get(ctx context.Context, owner, id domain.ID) (domain.Todo, error) {
	return s.repo.Todo(ctx, owner, id)
}

func (s *Todos) Find(ctx context.Context, owner domain.ID, f store.TodoFilter) ([]domain.Todo, error) {
	return s.repo.Todos(ctx, owner, f)
}

func (s *Todos) Scheduled(ctx context.Context, owner domain.ID, from, to time.Time) ([]domain.Todo, error) {
	return s.repo.Todos(ctx, owner, store.TodoFilter{OnCalendar: true, Scheduled: true, DueFrom: &from, DueTo: &to, Order: store.OrderByDue})
}

func (s *Todos) List(ctx context.Context, owner domain.ID, q TodoQuery) ([]domain.Todo, string, error) {
	var v domain.ValidationError
	f := store.TodoFilter{Order: store.OrderByID, Status: domain.TodoOpen}
	lists, _, err := s.listMap(ctx, owner)
	if err != nil {
		return nil, "", err
	}
	for _, raw := range q.Lists {
		id, err := domain.ParseID(raw)
		if _, ok := lists[id]; err != nil || !ok {
			v.Addf("list_id", "unknown list %q", raw)
			continue
		}
		f.Lists = append(f.Lists, id)
	}
	if q.Parent != "" {
		if id, err := domain.ParseID(q.Parent); err == nil {
			f.Parent = &id
		} else {
			v.Add("parent_id", "must be a todo id")
		}
	}
	switch q.Status {
	case "", "open":
	case "completed":
		f.Status = domain.TodoCompleted
	case "all":
		f.Status = ""
	default:
		v.Add("status", "must be open, completed or all")
	}
	f.DueFrom = optionalDate(&v, "due_from", q.DueFrom)
	f.DueTo = optionalDate(&v, "due_to", q.DueTo)
	if q.Cursor != "" {
		if id, err := domain.ParseID(q.Cursor); err == nil {
			f.After = &id
		} else {
			v.Add("cursor", "is not a valid cursor")
		}
	}
	limit := q.Limit
	switch {
	case limit == 0:
		limit = defaultTodoLimit
	case limit < 1 || limit > maxTodoLimit:
		v.Addf("limit", "must be between 1 and %d", maxTodoLimit)
	}
	if err := v.Err(); err != nil {
		return nil, "", err
	}
	f.Limit = limit + 1
	list, err := s.repo.Todos(ctx, owner, f)
	if err != nil {
		return nil, "", err
	}
	if len(list) > limit {
		list = list[:limit]
		return list, list[limit-1].ID.String(), nil
	}
	return list, "", nil
}

func optionalDate(v *domain.ValidationError, field, s string) *time.Time {
	if s == "" {
		return nil
	}
	d, ok := parseDate(s)
	if !ok {
		v.Add(field, "must be a date like 2026-09-12")
		return nil
	}
	return &d
}

func (s *Todos) Create(ctx context.Context, owner domain.ID, p domain.TodoPatch) (domain.Todo, error) {
	lists, def, err := s.listMap(ctx, owner)
	if err != nil {
		return domain.Todo{}, err
	}
	tz, _, err := userZone(ctx, s.users, owner)
	if err != nil {
		return domain.Todo{}, err
	}
	t := domain.Todo{ID: domain.NewID(), OwnerID: owner, ListID: def.ID, Status: domain.TodoOpen, TZ: tz, Version: 1}
	if err := applyTodo(&t, p, true, s.now()); err != nil {
		return domain.Todo{}, err
	}
	if def.ID == domain.NilID && !p.ListID.Set {
		return domain.Todo{}, fmt.Errorf("%w: the account has no todo list", domain.ErrConflict)
	}
	var out domain.Todo
	err = s.repo.WithTodos(ctx, owner, func(tx store.TodoTx) error {
		if err := relocate(ctx, tx, &t, lists, p.ListID, p.ParentID, nil, true); err != nil {
			return err
		}
		var err error
		out, err = tx.Insert(ctx, t)
		return err
	})
	return out, err
}

func (s *Todos) Update(ctx context.Context, owner, id domain.ID, p domain.TodoPatch, ifMatch string) (domain.Todo, error) {
	lists, _, err := s.listMap(ctx, owner)
	if err != nil {
		return domain.Todo{}, err
	}
	var out domain.Todo
	err = s.withTodo(ctx, owner, id, ifMatch, func(tx store.TodoTx, t *domain.Todo) error {
		if err := applyTodo(t, p, false, s.now()); err != nil {
			return err
		}
		if p.ListID.Set || p.ParentID.Set {
			if err := relocate(ctx, tx, t, lists, p.ListID, p.ParentID, nil, false); err != nil {
				return err
			}
		}
		var err error
		out, err = tx.Update(ctx, *t)
		return err
	})
	return out, err
}

func (s *Todos) Move(ctx context.Context, owner, id domain.ID, m domain.TodoMove, ifMatch string) (domain.Todo, error) {
	lists, _, err := s.listMap(ctx, owner)
	if err != nil {
		return domain.Todo{}, err
	}
	var after *domain.Opt[string]
	if m.AfterID.Set {
		a := m.AfterID
		after = &a
	}
	var out domain.Todo
	err = s.withTodo(ctx, owner, id, ifMatch, func(tx store.TodoTx, t *domain.Todo) error {
		if err := relocate(ctx, tx, t, lists, m.ListID, m.ParentID, after, false); err != nil {
			return err
		}
		var err error
		out, err = tx.Update(ctx, *t)
		return err
	})
	return out, err
}

func (s *Todos) Complete(ctx context.Context, owner, id domain.ID, ifMatch string) (domain.Todo, *domain.Todo, error) {
	var out domain.Todo
	var next *domain.Todo
	now := s.now()
	err := s.withTodo(ctx, owner, id, ifMatch, func(tx store.TodoTx, t *domain.Todo) error {
		if t.Done() {
			out = *t
			return nil
		}
		if t.RRule != "" && t.DueDate != nil {
			n, ok, err := nextTodo(ctx, tx, *t)
			if err != nil {
				return err
			}
			if ok {
				next = &n
			}
			t.RRule = ""
		}
		t.Status, t.CompletedAt = domain.TodoCompleted, &now
		var err error
		out, err = tx.Update(ctx, *t)
		return err
	})
	return out, next, err
}

func (s *Todos) Reopen(ctx context.Context, owner, id domain.ID, ifMatch string) (domain.Todo, error) {
	var out domain.Todo
	err := s.withTodo(ctx, owner, id, ifMatch, func(tx store.TodoTx, t *domain.Todo) error {
		if !t.Done() {
			out = *t
			return nil
		}
		t.Status, t.CompletedAt = domain.TodoOpen, nil
		var err error
		out, err = tx.Update(ctx, *t)
		return err
	})
	return out, err
}

func (s *Todos) Delete(ctx context.Context, owner, id domain.ID, ifMatch string) error {
	return s.withTodo(ctx, owner, id, ifMatch, func(tx store.TodoTx, t *domain.Todo) error {
		return tx.Delete(ctx, *t, s.now())
	})
}

func (s *Todos) Checks(ctx context.Context, owner, id domain.ID) ([]domain.Check, error) {
	t, err := s.repo.Todo(ctx, owner, id)
	return t.Checks, err
}

func (s *Todos) AddCheck(ctx context.Context, owner, id domain.ID, p domain.CheckPatch) (domain.Check, error) {
	var out domain.Check
	err := s.withTodo(ctx, owner, id, "", func(tx store.TodoTx, t *domain.Todo) error {
		if len(t.Checks) >= domain.MaxChecks {
			return fmt.Errorf("%w: a todo can have at most %d checklist items", domain.ErrConflict, domain.MaxChecks)
		}
		c := domain.Check{ID: domain.NewID(), TodoID: t.ID}
		if !p.Text.Set {
			var v domain.ValidationError
			v.Add("text", "is required")
			return v.Err()
		}
		if err := applyCheck(&c, p, true, s.now()); err != nil {
			return err
		}
		last := ""
		if n := len(t.Checks); n > 0 {
			last = t.Checks[n-1].Position
		}
		c.Position = domain.KeyBetween(last, "")
		var err error
		if out, err = tx.InsertCheck(ctx, c); err != nil {
			return err
		}
		_, err = tx.Touch(ctx, *t)
		return err
	})
	return out, err
}

func (s *Todos) UpdateCheck(ctx context.Context, owner, id, cid domain.ID, p domain.CheckPatch) (domain.Check, error) {
	var out domain.Check
	err := s.withTodo(ctx, owner, id, "", func(tx store.TodoTx, t *domain.Todo) error {
		i := slices.IndexFunc(t.Checks, func(c domain.Check) bool { return c.ID == cid })
		if i < 0 {
			return domain.ErrNotFound
		}
		c := t.Checks[i]
		if err := applyCheck(&c, p, false, s.now()); err != nil {
			return err
		}
		var err error
		if out, err = tx.UpdateCheck(ctx, c); err != nil {
			return err
		}
		_, err = tx.Touch(ctx, *t)
		return err
	})
	return out, err
}

func (s *Todos) DeleteCheck(ctx context.Context, owner, id, cid domain.ID) error {
	return s.withTodo(ctx, owner, id, "", func(tx store.TodoTx, t *domain.Todo) error {
		if err := tx.DeleteCheck(ctx, t.ID, cid); err != nil {
			return err
		}
		_, err := tx.Touch(ctx, *t)
		return err
	})
}

func (s *Todos) ReorderChecks(ctx context.Context, owner, id domain.ID, ids []string) ([]domain.Check, error) {
	var out []domain.Check
	err := s.withTodo(ctx, owner, id, "", func(tx store.TodoTx, t *domain.Todo) error {
		var v domain.ValidationError
		byID := make(map[domain.ID]domain.Check, len(t.Checks))
		for _, c := range t.Checks {
			byID[c.ID] = c
		}
		order := make([]domain.Check, 0, len(ids))
		for _, raw := range ids {
			cid, err := domain.ParseID(raw)
			c, ok := byID[cid]
			if err != nil || !ok {
				break
			}
			delete(byID, cid)
			order = append(order, c)
		}
		if len(order) != len(ids) || len(byID) != 0 {
			v.Add("ids", "must list every checklist item exactly once")
			return v.Err()
		}
		prev := ""
		for _, c := range order {
			c.Position = domain.KeyBetween(prev, "")
			prev = c.Position
			saved, err := tx.UpdateCheck(ctx, c)
			if err != nil {
				return err
			}
			out = append(out, saved)
		}
		_, err := tx.Touch(ctx, *t)
		return err
	})
	return out, err
}

func (s *Todos) withTodo(ctx context.Context, owner, id domain.ID, ifMatch string, fn func(store.TodoTx, *domain.Todo) error) error {
	return s.repo.WithTodos(ctx, owner, func(tx store.TodoTx) error {
		t, err := tx.Get(ctx, id)
		if err != nil {
			return err
		}
		if err := domain.CheckIfMatch(ifMatch, t.ETag()); err != nil {
			return err
		}
		return fn(tx, &t)
	})
}

func (s *Todos) listMap(ctx context.Context, owner domain.ID) (map[domain.ID]domain.TodoList, domain.TodoList, error) {
	list, err := s.repo.ListTodoLists(ctx, owner)
	if err != nil {
		return nil, domain.TodoList{}, err
	}
	m, def := indexByID(list, func(l domain.TodoList) (domain.ID, bool) { return l.ID, l.IsDefault })
	return m, def, nil
}

func relocate(ctx context.Context, tx store.TodoTx, t *domain.Todo, lists map[domain.ID]domain.TodoList, listOpt, parentOpt domain.Opt[string], after *domain.Opt[string], isNew bool) error {
	var v domain.ValidationError
	list, parent := t.ListID, t.ParentID
	listRaw := strings.TrimSpace(listOpt.V)
	listGiven := listOpt.Set && !listOpt.Null && listRaw != ""
	if listGiven {
		id, err := domain.ParseID(listRaw)
		if _, ok := lists[id]; err != nil || !ok {
			v.Add("list_id", "unknown list")
			return v.Err()
		}
		list = id
	}
	switch {
	case parentOpt.Set:
		parent = nil
		if raw := strings.TrimSpace(parentOpt.V); !parentOpt.Null && raw != "" {
			p, err := lookupTodo(ctx, tx, raw)
			if err != nil {
				if !errors.Is(err, domain.ErrNotFound) {
					return err
				}
				v.Add("parent_id", "unknown todo")
				return v.Err()
			}
			switch {
			case p.ID == t.ID:
				v.Add("parent_id", "must not be the todo itself")
			case p.ParentID != nil:
				v.Add("parent_id", "must be a todo without a parent, subtasks go one level deep")
			case listGiven && list != p.ListID:
				v.Add("list_id", "must match the list of the parent")
			}
			if err := v.Err(); err != nil {
				return err
			}
			parent, list = &p.ID, p.ListID
		}
	case t.ParentID != nil && list != t.ListID:
		v.Add("list_id", "subtasks move with their parent, set parent_id to null to move this todo alone")
		return v.Err()
	}
	if parent != nil && !isNew {
		has, err := tx.HasChildren(ctx, t.ID)
		if err != nil {
			return err
		}
		if has {
			v.Add("parent_id", "a todo with subtasks cannot become a subtask")
			return v.Err()
		}
	}
	pos := t.Position
	switch {
	case after != nil:
		p, err := positionAfter(ctx, tx, t.ID, list, parent, *after)
		if err != nil {
			return err
		}
		pos = p
	case isNew || list != t.ListID || !sameParent(parent, t.ParentID):
		last, err := tx.LastPosition(ctx, list, parent, t.ID)
		if err != nil {
			return err
		}
		pos = domain.KeyBetween(last, "")
	}
	t.ListID, t.ParentID, t.Position = list, parent, pos
	return nil
}

func positionAfter(ctx context.Context, tx store.TodoTx, self, list domain.ID, parent *domain.ID, after domain.Opt[string]) (string, error) {
	raw := strings.TrimSpace(after.V)
	if after.Null || raw == "" {
		next, err := tx.NextPosition(ctx, list, parent, "", self)
		return domain.KeyBetween("", next), err
	}
	a, err := lookupTodo(ctx, tx, raw)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return "", err
	}
	if err != nil || a.ID == self || a.ListID != list || !sameParent(a.ParentID, parent) {
		var v domain.ValidationError
		v.Add("after_id", "must be a todo in the same list and under the same parent")
		return "", v.Err()
	}
	next, err := tx.NextPosition(ctx, list, parent, a.Position, self)
	return domain.KeyBetween(a.Position, next), err
}

func lookupTodo(ctx context.Context, tx store.TodoTx, raw string) (domain.Todo, error) {
	id, err := domain.ParseID(raw)
	if err != nil {
		return domain.Todo{}, domain.ErrNotFound
	}
	return tx.Get(ctx, id)
}

func nextTodo(ctx context.Context, tx store.TodoTx, t domain.Todo) (domain.Todo, bool, error) {
	start, _ := t.DueStart()
	at, rule, ok, err := recurrence.Next(t.RRule, start)
	if err != nil || !ok {
		return domain.Todo{}, false, err
	}
	y, m, d := at.In(start.Location()).Date()
	due := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	n := t
	n.ID, n.Status, n.CompletedAt, n.Version = domain.NewID(), domain.TodoOpen, nil, 1
	n.DueDate, n.RRule = &due, rule
	n.Reminders = slices.Clone(t.Reminders)
	n.Checks = make([]domain.Check, len(t.Checks))
	for i, c := range t.Checks {
		n.Checks[i] = domain.Check{ID: domain.NewID(), TodoID: n.ID, Text: c.Text, Position: c.Position}
	}
	after, err := tx.NextPosition(ctx, t.ListID, t.ParentID, t.Position, t.ID)
	if err != nil {
		return domain.Todo{}, false, err
	}
	n.Position = domain.KeyBetween(t.Position, after)
	created, err := tx.Insert(ctx, n)
	return created, err == nil, err
}

func sameParent(a, b *domain.ID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
