package pg

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Alexander-D-Karpov/calendar/internal/db"
	"github.com/Alexander-D-Karpov/calendar/internal/db/sqlc"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/store"
)

const todoColumns = `id, owner_id, list_id, parent_id, title, body, status, priority, position, due_date, due_time,
	duration_min, tz, rrule, show_on_calendar, completed_at, version, created_at, updated_at, deleted_at, coalesce(ical_uid, '')`

const checkColumns = `id, todo_id, text, done, position, done_at, created_at`

const (
	selectTodoSQL = `SELECT ` + todoColumns + ` FROM todos WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL`

	insertTodoSQL = `INSERT INTO todos (id, owner_id, list_id, parent_id, title, body, status, priority, position,
		due_date, due_time, duration_min, tz, rrule, show_on_calendar, completed_at, fingerprint, ical_uid)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
	RETURNING ` + todoColumns

	updateTodoSQL = `WITH old AS (SELECT due_date AS od, due_time AS ot, duration_min AS odur, tz AS otz, list_id AS olist
		FROM todos WHERE id = $1 AND owner_id = $2)
	UPDATE todos SET list_id = $3, parent_id = $4, title = $5, body = $6, status = $7, priority = $8, position = $9,
		due_date = $10, due_time = $11, duration_min = $12, tz = $13, rrule = $14, show_on_calendar = $15,
		completed_at = $16, fingerprint = $17, version = version + 1, updated_at = now()
	FROM old
	WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL
	RETURNING ` + todoColumns + `, od, ot, odur, otz, olist`

	deleteTodoSQL = `UPDATE todos SET deleted_at = $3, version = version + 1, updated_at = now()
		WHERE owner_id = $1 AND (id = $2 OR parent_id = $2) AND deleted_at IS NULL`

	touchTodoSQL = `UPDATE todos SET version = version + 1, updated_at = now()
		WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL RETURNING version, updated_at`

	hasChildrenSQL = `SELECT EXISTS (SELECT 1 FROM todos WHERE parent_id = $1 AND deleted_at IS NULL)`

	lastPosSQL = `SELECT coalesce(max(position), '') FROM todos
		WHERE owner_id = $1 AND list_id = $2 AND parent_id IS NOT DISTINCT FROM $3 AND id <> $4 AND deleted_at IS NULL`

	nextPosSQL = `SELECT coalesce(min(position), '') FROM todos
		WHERE owner_id = $1 AND list_id = $2 AND parent_id IS NOT DISTINCT FROM $3 AND id <> $4
		AND position > $5 AND deleted_at IS NULL`

	checksSQL      = `SELECT ` + checkColumns + ` FROM todo_checks WHERE todo_id = ANY($1) ORDER BY position, id`
	insertCheckSQL = `INSERT INTO todo_checks (id, todo_id, text, done, position, done_at) VALUES ($1, $2, $3, $4, $5, $6) RETURNING ` + checkColumns
	updateCheckSQL = `UPDATE todo_checks SET text = $3, done = $4, position = $5, done_at = $6 WHERE id = $1 AND todo_id = $2 RETURNING ` + checkColumns
	deleteCheckSQL = `DELETE FROM todo_checks WHERE id = $1 AND todo_id = $2`
)

var todoOrders = map[store.TodoOrder]string{
	store.OrderByID:        ` ORDER BY id`,
	store.OrderByPosition:  ` ORDER BY list_id, parent_id NULLS FIRST, position, id`,
	store.OrderByDue:       ` ORDER BY due_date, due_time NULLS FIRST, position, id`,
	store.OrderByCompleted: ` ORDER BY completed_at DESC, id`,
}

func setDue(t *domain.Todo, due pgtype.Date, tm pgtype.Time, dur *int32, tz *string) {
	if due.Valid {
		d := time.Date(due.Time.Year(), due.Time.Month(), due.Time.Day(), 0, 0, 0, 0, time.UTC)
		t.DueDate = &d
	}
	if tm.Valid {
		m := int(tm.Microseconds / int64(time.Minute/time.Microsecond))
		t.DueTime = &m
	}
	if dur != nil {
		t.Duration = int(*dur)
	}
	t.TZ = text(tz)
}

func dueArgs(t domain.Todo) (pgtype.Date, pgtype.Time, *int32) {
	var due pgtype.Date
	var tm pgtype.Time
	var dur *int32
	if t.DueDate != nil {
		due = pgDate(*t.DueDate)
	}
	if t.DueTime != nil {
		tm = pgtype.Time{Microseconds: int64(*t.DueTime) * int64(time.Minute/time.Microsecond), Valid: true}
	}
	if t.Duration > 0 {
		d := int32(t.Duration)
		dur = &d
	}
	return due, tm, dur
}

func scanTodo(row pgx.Row, extra ...any) (domain.Todo, error) {
	var (
		t        domain.Todo
		prio     int16
		due      pgtype.Date
		tm       pgtype.Time
		dur      *int32
		tz, rule *string
	)
	dest := append([]any{
		&t.ID, &t.OwnerID, &t.ListID, &t.ParentID, &t.Title, &t.Body, &t.Status, &prio, &t.Position, &due, &tm,
		&dur, &tz, &rule, &t.ShowOnCalendar, &t.CompletedAt, &t.Version, &t.CreatedAt, &t.UpdatedAt, &t.DeletedAt,
		&t.UID,
	}, extra...)
	if err := row.Scan(dest...); err != nil {
		return domain.Todo{}, err
	}
	setDue(&t, due, tm, dur, tz)
	t.Priority, t.RRule = int(prio), text(rule)
	return t, nil
}

func scanCheck(row pgx.Row) (domain.Check, error) {
	var c domain.Check
	err := row.Scan(&c.ID, &c.TodoID, &c.Text, &c.Done, &c.Position, &c.DoneAt, &c.CreatedAt)
	return c, err
}

func listTodos(ctx context.Context, q db.DBTX, sql string, args ...any) ([]domain.Todo, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	var out []domain.Todo
	for rows.Next() {
		t, err := scanTodo(rows)
		if err != nil {
			rows.Close()
			return nil, mapErr(err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return out, mapErr(loadTodoExtras(ctx, q, out))
}

func loadTodoExtras(ctx context.Context, q db.DBTX, todos []domain.Todo) error {
	if len(todos) == 0 {
		return nil
	}
	ids := make([]domain.ID, len(todos))
	idx := make(map[domain.ID]int, len(todos))
	for i := range todos {
		ids[i] = todos[i].ID
		idx[todos[i].ID] = i
		todos[i].Checks = []domain.Check{}
	}
	rems, err := reminderMap(ctx, q, todoReminders, ids)
	if err != nil {
		return err
	}
	rows, err := q.Query(ctx, checksSQL, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return err
		}
		i := idx[c.TodoID]
		todos[i].Checks = append(todos[i].Checks, c)
	}
	for i := range todos {
		todos[i].Reminders = append([]int{}, rems[todos[i].ID]...)
	}
	return rows.Err()
}

func todoQuery(owner domain.ID, f store.TodoFilter) (string, []any) {
	var b strings.Builder
	args := []any{owner}
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	b.WriteString(`SELECT ` + todoColumns + ` FROM todos WHERE owner_id = $1 AND deleted_at IS NULL`)
	if len(f.Lists) > 0 {
		b.WriteString(` AND list_id = ANY(` + arg(f.Lists) + `)`)
	}
	switch {
	case f.Parent != nil:
		b.WriteString(` AND parent_id = ` + arg(*f.Parent))
	case f.TopLevel:
		b.WriteString(` AND parent_id IS NULL`)
	}
	if f.Status != "" {
		b.WriteString(` AND status = ` + arg(f.Status))
	}
	if f.Scheduled {
		b.WriteString(` AND due_date IS NOT NULL`)
	}
	if f.OnCalendar {
		b.WriteString(` AND show_on_calendar`)
	}
	if f.DueFrom != nil {
		b.WriteString(` AND due_date >= ` + arg(pgDate(*f.DueFrom)))
	}
	if f.DueTo != nil {
		b.WriteString(` AND due_date < ` + arg(pgDate(*f.DueTo)))
	}
	if f.After != nil {
		b.WriteString(` AND id > ` + arg(*f.After))
	}
	b.WriteString(todoOrders[f.Order])
	if f.Limit > 0 {
		b.WriteString(` LIMIT ` + arg(f.Limit))
	}
	return b.String(), args
}

func oneTodo(list []domain.Todo, err error) (domain.Todo, error) {
	if err != nil {
		return domain.Todo{}, err
	}
	if len(list) == 0 {
		return domain.Todo{}, domain.ErrNotFound
	}
	return list[0], nil
}

func (s *Store) Todo(ctx context.Context, owner, id domain.ID) (domain.Todo, error) {
	return oneTodo(listTodos(ctx, s.pool, selectTodoSQL, id, owner))
}

func (s *Store) Todos(ctx context.Context, owner domain.ID, f store.TodoFilter) ([]domain.Todo, error) {
	sql, args := todoQuery(owner, f)
	return listTodos(ctx, s.pool, sql, args...)
}

func (s *Store) WithTodos(ctx context.Context, owner domain.ID, fn func(store.TodoTx) error) error {
	return mapErr(s.rawTx(ctx, func(tx pgx.Tx, q *sqlc.Queries) error {
		return fn(&todoTx{tx: tx, q: q, owner: owner})
	}))
}

type todoTx struct {
	tx    pgx.Tx
	q     *sqlc.Queries
	owner domain.ID
}

func (t *todoTx) Get(ctx context.Context, id domain.ID) (domain.Todo, error) {
	return oneTodo(listTodos(ctx, t.tx, selectTodoSQL+` FOR UPDATE`, id, t.owner))
}

func (t *todoTx) Insert(ctx context.Context, x domain.Todo) (domain.Todo, error) {
	due, tm, dur := dueArgs(x)
	out, err := scanTodo(t.tx.QueryRow(ctx, insertTodoSQL,
		x.ID, t.owner, x.ListID, x.ParentID, x.Title, x.Body, x.Status, int16(x.Priority), x.Position,
		due, tm, dur, textPtr(x.TZ), textPtr(x.RRule), x.ShowOnCalendar, x.CompletedAt, domain.TodoFingerprint(x), textPtr(x.UID)))
	if err != nil {
		return domain.Todo{}, mapErr(err)
	}
	if out.Reminders, err = replaceReminders(ctx, t.tx, todoReminders, out.ID, x.Reminders); err != nil {
		return domain.Todo{}, mapErr(err)
	}
	out.Checks = make([]domain.Check, 0, len(x.Checks))
	for _, c := range x.Checks {
		c.TodoID = out.ID
		saved, err := t.InsertCheck(ctx, c)
		if err != nil {
			return domain.Todo{}, err
		}
		out.Checks = append(out.Checks, saved)
	}
	from, to := todoRange(out)
	return out, mapErr(t.change(ctx, out, domain.OpCreate, from, to))
}

func (t *todoTx) Update(ctx context.Context, x domain.Todo) (domain.Todo, error) {
	due, tm, dur := dueArgs(x)
	var (
		od    pgtype.Date
		ot    pgtype.Time
		odur  *int32
		otz   *string
		olist domain.ID
	)
	out, err := scanTodo(t.tx.QueryRow(ctx, updateTodoSQL,
		x.ID, t.owner, x.ListID, x.ParentID, x.Title, x.Body, x.Status, int16(x.Priority), x.Position,
		due, tm, dur, textPtr(x.TZ), textPtr(x.RRule), x.ShowOnCalendar, x.CompletedAt, domain.TodoFingerprint(x)),
		&od, &ot, &odur, &otz, &olist)
	if err != nil {
		return domain.Todo{}, mapErr(err)
	}
	if out.Reminders, err = replaceReminders(ctx, t.tx, todoReminders, out.ID, x.Reminders); err != nil {
		return domain.Todo{}, mapErr(err)
	}
	out.Checks = x.Checks
	if olist != out.ListID {
		if err := queueSync(ctx, t.tx, t.owner, domain.EntityTodo, out.ID, domain.OpDelete, nil, &olist); err != nil {
			return domain.Todo{}, mapErr(err)
		}
	}
	var old domain.Todo
	setDue(&old, od, ot, odur, otz)
	af, at := todoRange(old)
	bf, bt := todoRange(out)
	from, to := mergeRange(af, at, bf, bt)
	return out, mapErr(t.change(ctx, out, domain.OpUpdate, from, to))
}

func (t *todoTx) Delete(ctx context.Context, x domain.Todo, at time.Time) error {
	tag, err := t.tx.Exec(ctx, deleteTodoSQL, t.owner, x.ID, at)
	if err := affected(tag.RowsAffected(), err); err != nil {
		return err
	}
	from, to := todoRange(x)
	return mapErr(t.change(ctx, x, domain.OpDelete, from, to))
}

func (t *todoTx) Touch(ctx context.Context, x domain.Todo) (domain.Todo, error) {
	if err := t.tx.QueryRow(ctx, touchTodoSQL, x.ID, t.owner).Scan(&x.Version, &x.UpdatedAt); err != nil {
		return domain.Todo{}, mapErr(err)
	}
	from, to := todoRange(x)
	return x, mapErr(t.change(ctx, x, domain.OpUpdate, from, to))
}

func (t *todoTx) HasChildren(ctx context.Context, id domain.ID) (bool, error) {
	var ok bool
	err := t.tx.QueryRow(ctx, hasChildrenSQL, id).Scan(&ok)
	return ok, mapErr(err)
}

func (t *todoTx) LastPosition(ctx context.Context, list domain.ID, parent *domain.ID, exclude domain.ID) (string, error) {
	var pos string
	err := t.tx.QueryRow(ctx, lastPosSQL, t.owner, list, parent, exclude).Scan(&pos)
	return pos, mapErr(err)
}

func (t *todoTx) NextPosition(ctx context.Context, list domain.ID, parent *domain.ID, after string, exclude domain.ID) (string, error) {
	var pos string
	err := t.tx.QueryRow(ctx, nextPosSQL, t.owner, list, parent, exclude, after).Scan(&pos)
	return pos, mapErr(err)
}

func (t *todoTx) InsertCheck(ctx context.Context, c domain.Check) (domain.Check, error) {
	out, err := scanCheck(t.tx.QueryRow(ctx, insertCheckSQL, c.ID, c.TodoID, c.Text, c.Done, c.Position, c.DoneAt))
	return out, mapErr(err)
}

func (t *todoTx) UpdateCheck(ctx context.Context, c domain.Check) (domain.Check, error) {
	out, err := scanCheck(t.tx.QueryRow(ctx, updateCheckSQL, c.ID, c.TodoID, c.Text, c.Done, c.Position, c.DoneAt))
	return out, mapErr(err)
}

func (t *todoTx) DeleteCheck(ctx context.Context, todo, id domain.ID) error {
	tag, err := t.tx.Exec(ctx, deleteCheckSQL, id, todo)
	return affected(tag.RowsAffected(), err)
}

func (t *todoTx) change(ctx context.Context, x domain.Todo, op string, from, to *time.Time) error {
	list := x.ListID
	err := recordChange(ctx, t.q, domain.Change{
		OwnerID: t.owner, Entity: domain.EntityTodo, EntityID: x.ID, Op: op, ListID: &list, From: from, To: to,
	})
	if err != nil {
		return err
	}
	return queueSync(ctx, t.tx, t.owner, domain.EntityTodo, x.ID, op, nil, &list)
}

func todoRange(t domain.Todo) (*time.Time, *time.Time) {
	start, end, allDay, ok := t.Span()
	if !ok {
		return nil, nil
	}
	if allDay {
		start, end = start.Add(-14*time.Hour), end.Add(14*time.Hour)
	}
	s, e := start.UTC(), end.UTC()
	return &s, &e
}

func mergeRange(af, at, bf, bt *time.Time) (*time.Time, *time.Time) {
	if af == nil {
		return bf, bt
	}
	if bf == nil {
		return af, at
	}
	from, to := *af, *at
	if bf.Before(from) {
		from = *bf
	}
	if bt.After(to) {
		to = *bt
	}
	return &from, &to
}
