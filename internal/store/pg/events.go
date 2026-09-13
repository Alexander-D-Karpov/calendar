package pg

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Alexander-D-Karpov/calendar/internal/db"
	"github.com/Alexander-D-Karpov/calendar/internal/db/sqlc"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/recurrence"
	"github.com/Alexander-D-Karpov/calendar/internal/store"
)

const eventColumns = `id, owner_id, calendar_id, ical_uid, series_id, recurrence_id, title, body, location, url, all_day,
	start_at, end_at, start_date, end_date, tz, rrule, rdate, exdate, status, transparency, visibility, color,
	read_only, sequence, version, created_at, updated_at, deleted_at, source_hash`

const (
	selectEventSQL = `SELECT ` + eventColumns + ` FROM events
		WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL`

	rangeEventsSQL = `SELECT ` + eventColumns + ` FROM events
		WHERE owner_id = $1 AND calendar_id = ANY($2) AND deleted_at IS NULL AND span && tstzrange($3, $4)`

	overridesOfSQL = `SELECT ` + eventColumns + ` FROM events
		WHERE owner_id = $1 AND series_id = ANY($2) AND deleted_at IS NULL`

	overrideSQL = `SELECT ` + eventColumns + ` FROM events
		WHERE owner_id = $1 AND series_id = $2 AND recurrence_id = $3 AND deleted_at IS NULL FOR UPDATE`

	seriesOverridesSQL = `SELECT ` + eventColumns + ` FROM events
		WHERE owner_id = $1 AND series_id = $2 AND deleted_at IS NULL ORDER BY recurrence_id FOR UPDATE`

	insertEventSQL = `INSERT INTO events (id, owner_id, calendar_id, ical_uid, series_id, recurrence_id, title, body,
		location, url, all_day, start_at, end_at, start_date, end_date, tz, span, rrule, rdate, exdate, status,
		transparency, visibility, color, read_only, sequence, fingerprint, source_hash)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22,
		$23, $24, $25, $26, $27, $28)
	RETURNING ` + eventColumns

	updateEventSQL = `WITH old AS (SELECT span AS old_span, calendar_id AS old_cal FROM events WHERE id = $1 AND owner_id = $2)
	UPDATE events SET calendar_id = $3, series_id = $4, recurrence_id = $5, title = $6, body = $7, location = $8,
		url = $9, all_day = $10, start_at = $11, end_at = $12, start_date = $13, end_date = $14, tz = $15,
		span = $16, rrule = $17, rdate = $18, exdate = $19, status = $20, transparency = $21, visibility = $22,
		color = $23, sequence = $24, read_only = $25, fingerprint = $26, source_hash = $27,
		version = version + 1, updated_at = now()
	FROM old
	WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL
	RETURNING ` + eventColumns + `, range_merge(old_span, span), old_cal`

	deleteEventSQL = `UPDATE events SET deleted_at = $3, version = version + 1, updated_at = now()
		WHERE owner_id = $1 AND (id = $2 OR series_id = $2) AND deleted_at IS NULL`

	moveOverridesSQL = `UPDATE events SET series_id = $3, calendar_id = $4, ical_uid = $5, version = version + 1, updated_at = now()
		WHERE owner_id = $1 AND series_id = $2 AND recurrence_id >= $6 AND deleted_at IS NULL`
)

type eventRow struct {
	startAt, endAt     *time.Time
	startDate, endDate pgtype.Date
	tz                 *string
	span               pgtype.Range[pgtype.Timestamptz]
	rdate, exdate      []time.Time
	fp                 []byte
}

func toEventRow(e domain.Event) eventRow {
	from, to := recurrence.Span(e)
	w := eventRow{span: tstzrange(&from, to), rdate: nonNilTimes(e.RDate), exdate: nonNilTimes(e.ExDate), fp: domain.EventFingerprint(e)}
	if e.AllDay {
		w.startDate = pgtype.Date{Time: e.Start.UTC(), Valid: true}
		w.endDate = pgtype.Date{Time: e.End.UTC(), Valid: true}
		return w
	}
	s, en := e.Start.UTC(), e.End.UTC()
	w.startAt, w.endAt, w.tz = &s, &en, textPtr(e.TZ)
	return w
}

func scanEvent(row pgx.Row, extra ...any) (domain.Event, error) {
	var (
		e                                  domain.Event
		startAt, endAt, startDate, endDate *time.Time
		tz, rule, color                    *string
		seq                                int32
	)
	dest := append([]any{
		&e.ID, &e.OwnerID, &e.CalendarID, &e.UID, &e.SeriesID, &e.RecurrenceID, &e.Title, &e.Body, &e.Location,
		&e.URL, &e.AllDay, &startAt, &endAt, &startDate, &endDate, &tz, &rule, &e.RDate, &e.ExDate, &e.Status,
		&e.Transparency, &e.Visibility, &color, &e.ReadOnly, &seq, &e.Version, &e.CreatedAt, &e.UpdatedAt, &e.DeletedAt,
		&e.SourceHash,
	}, extra...)
	if err := row.Scan(dest...); err != nil {
		return domain.Event{}, err
	}
	switch {
	case e.AllDay && startDate != nil && endDate != nil:
		e.Start, e.End = startDate.UTC(), endDate.UTC()
	case startAt != nil && endAt != nil:
		e.Start, e.End = *startAt, *endAt
	}
	e.TZ, e.RRule, e.Color, e.Sequence = text(tz), text(rule), text(color), int(seq)
	return e, nil
}

func listEvents(ctx context.Context, q db.DBTX, sql string, args ...any) ([]domain.Event, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []domain.Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, mapErr(err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return out, mapErr(loadReminders(ctx, q, out))
}

func loadReminders(ctx context.Context, q db.DBTX, events []domain.Event) error {
	if len(events) == 0 {
		return nil
	}
	ids := make([]domain.ID, len(events))
	for i := range events {
		ids[i] = events[i].ID
	}
	m, err := reminderMap(ctx, q, eventReminders, ids)
	if err != nil {
		return err
	}
	for i := range events {
		events[i].Reminders = append([]int{}, m[events[i].ID]...)
	}
	return nil
}

func (s *Store) Event(ctx context.Context, owner, id domain.ID) (domain.Event, error) {
	list, err := listEvents(ctx, s.pool, selectEventSQL, id, owner)
	if err != nil {
		return domain.Event{}, err
	}
	if len(list) == 0 {
		return domain.Event{}, domain.ErrNotFound
	}
	return list[0], nil
}

func (s *Store) EventsInRange(ctx context.Context, owner domain.ID, calendars []domain.ID, from, to time.Time) ([]domain.Event, error) {
	return listEvents(ctx, s.pool, rangeEventsSQL, owner, calendars, from, to)
}

func (s *Store) OverridesOf(ctx context.Context, owner domain.ID, series []domain.ID) ([]domain.Event, error) {
	if len(series) == 0 {
		return nil, nil
	}
	return listEvents(ctx, s.pool, overridesOfSQL, owner, series)
}

func (s *Store) WithEvents(ctx context.Context, owner domain.ID, fn func(store.EventTx) error) error {
	return mapErr(db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		return fn(&eventTx{tx: tx, q: s.q.WithTx(tx), owner: owner})
	}))
}

type eventTx struct {
	tx    pgx.Tx
	q     *sqlc.Queries
	owner domain.ID
}

func (t *eventTx) Get(ctx context.Context, id domain.ID) (domain.Event, error) {
	return t.one(ctx, selectEventSQL+` FOR UPDATE`, id, t.owner)
}

func (t *eventTx) Override(ctx context.Context, series domain.ID, at time.Time) (domain.Event, error) {
	return t.one(ctx, overrideSQL, t.owner, series, at)
}

func (t *eventTx) Overrides(ctx context.Context, series domain.ID) ([]domain.Event, error) {
	return listEvents(ctx, t.tx, seriesOverridesSQL, t.owner, series)
}

func (t *eventTx) Insert(ctx context.Context, e domain.Event) (domain.Event, error) {
	w := toEventRow(e)
	out, err := scanEvent(t.tx.QueryRow(ctx, insertEventSQL,
		e.ID, t.owner, e.CalendarID, e.UID, e.SeriesID, e.RecurrenceID, e.Title, e.Body, e.Location, e.URL,
		e.AllDay, w.startAt, w.endAt, w.startDate, w.endDate, w.tz, w.span, textPtr(e.RRule), w.rdate, w.exdate,
		e.Status, e.Transparency, e.Visibility, textPtr(e.Color), e.ReadOnly, int32(e.Sequence), w.fp, e.SourceHash))
	if err != nil {
		return domain.Event{}, mapErr(err)
	}
	if err := t.saveReminders(ctx, &out, e.Reminders); err != nil {
		return domain.Event{}, mapErr(err)
	}
	from, to := recurrence.Span(out)
	return out, mapErr(t.change(ctx, out, domain.OpCreate, &from, to))
}

func (t *eventTx) Update(ctx context.Context, e domain.Event) (domain.Event, error) {
	w := toEventRow(e)
	var merged pgtype.Range[pgtype.Timestamptz]
	var oldCal domain.ID
	out, err := scanEvent(t.tx.QueryRow(ctx, updateEventSQL,
		e.ID, t.owner, e.CalendarID, e.SeriesID, e.RecurrenceID, e.Title, e.Body, e.Location, e.URL, e.AllDay,
		w.startAt, w.endAt, w.startDate, w.endDate, w.tz, w.span, textPtr(e.RRule), w.rdate, w.exdate, e.Status,
		e.Transparency, e.Visibility, textPtr(e.Color), int32(e.Sequence), e.ReadOnly, w.fp, e.SourceHash), &merged, &oldCal)
	if err != nil {
		return domain.Event{}, mapErr(err)
	}
	if err := t.saveReminders(ctx, &out, e.Reminders); err != nil {
		return domain.Event{}, mapErr(err)
	}
	if oldCal != out.CalendarID {
		if err := queueSync(ctx, t.tx, t.owner, domain.EntityEvent, out.ID, domain.OpDelete, &oldCal, nil); err != nil {
			return domain.Event{}, mapErr(err)
		}
	}
	from, to := rangeBounds(merged)
	return out, mapErr(t.change(ctx, out, domain.OpUpdate, from, to))
}

func (t *eventTx) Delete(ctx context.Context, e domain.Event, at time.Time) error {
	tag, err := t.tx.Exec(ctx, deleteEventSQL, t.owner, e.ID, at)
	if err := affected(tag.RowsAffected(), err); err != nil {
		return err
	}
	from, to := recurrence.Span(e)
	return mapErr(t.change(ctx, e, domain.OpDelete, &from, to))
}

func (t *eventTx) MoveOverrides(ctx context.Context, from domain.ID, to domain.Event, since time.Time) error {
	_, err := t.tx.Exec(ctx, moveOverridesSQL, t.owner, from, to.ID, to.CalendarID, to.UID, since)
	return mapErr(err)
}

func (t *eventTx) one(ctx context.Context, sql string, args ...any) (domain.Event, error) {
	list, err := listEvents(ctx, t.tx, sql, args...)
	if err != nil {
		return domain.Event{}, err
	}
	if len(list) == 0 {
		return domain.Event{}, domain.ErrNotFound
	}
	return list[0], nil
}

func (t *eventTx) saveReminders(ctx context.Context, e *domain.Event, mins []int) error {
	out, err := replaceReminders(ctx, t.tx, eventReminders, e.ID, mins)
	e.Reminders = out
	return err
}

func (t *eventTx) change(ctx context.Context, e domain.Event, op string, from, to *time.Time) error {
	cal := e.CalendarID
	err := recordChange(ctx, t.q, domain.Change{
		OwnerID: t.owner, Entity: domain.EntityEvent, EntityID: e.ID, Op: op, CalendarID: &cal, From: from, To: to,
	})
	if err != nil {
		return err
	}
	return queueSync(ctx, t.tx, t.owner, domain.EntityEvent, e.ID, op, &cal, nil)
}

func nonNilTimes(ts []time.Time) []time.Time {
	if ts == nil {
		return []time.Time{}
	}
	return ts
}

func eventSpan(e domain.Event) (*time.Time, *time.Time) {
	from, to := recurrence.Span(e)
	return &from, to
}
