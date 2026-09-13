package pg

import (
	"context"
	"slices"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/db"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

// Event and todo reminders live in matching tables that differ only in the
// column pointing back at the owner, so the helpers below take the pair.
type reminderTable struct {
	name string
	col  string
}

var (
	eventReminders = reminderTable{name: "event_reminders", col: "event_id"}
	todoReminders  = reminderTable{name: "todo_reminders", col: "todo_id"}
)

func reminderMap(ctx context.Context, q db.DBTX, t reminderTable, ids []domain.ID) (map[domain.ID][]int, error) {
	if len(ids) == 0 {
		return map[domain.ID][]int{}, nil
	}
	sql := `SELECT ` + t.col + `, minutes_before FROM ` + t.name +
		` WHERE ` + t.col + ` = ANY($1) ORDER BY ` + t.col + `, minutes_before`
	rows, err := q.Query(ctx, sql, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[domain.ID][]int{}
	for rows.Next() {
		var id domain.ID
		var mins int32
		if err := rows.Scan(&id, &mins); err != nil {
			return nil, err
		}
		out[id] = append(out[id], int(mins))
	}
	return out, rows.Err()
}

func replaceReminders(ctx context.Context, q db.DBTX, t reminderTable, id domain.ID, mins []int) ([]int, error) {
	if _, err := q.Exec(ctx, `DELETE FROM `+t.name+` WHERE `+t.col+` = $1`, id); err != nil {
		return nil, err
	}
	out := slices.Compact(slices.Sorted(slices.Values(mins)))
	if out == nil {
		out = []int{}
	}
	if len(out) == 0 {
		return out, nil
	}
	each := make([]int32, len(out))
	for i, m := range out {
		each[i] = int32(m)
	}
	_, err := q.Exec(ctx, `INSERT INTO `+t.name+` (`+t.col+`, minutes_before)
		SELECT $1, m FROM unnest($2::int[]) AS m`, id, each)
	return out, err
}

const (
	remindableEventsSQL = `SELECT ` + eventColumns + ` FROM events
		WHERE deleted_at IS NULL AND status <> 'cancelled' AND span && tstzrange($1, $2)
		AND EXISTS (SELECT 1 FROM event_reminders r WHERE r.event_id = events.id)
		ORDER BY id LIMIT $3`

	remindableTodosSQL = `SELECT ` + todoColumns + ` FROM todos
		WHERE deleted_at IS NULL AND status = 'needs_action' AND due_date BETWEEN $1 AND $2
		AND EXISTS (SELECT 1 FROM todo_reminders r WHERE r.todo_id = todos.id)
		ORDER BY id LIMIT $3`

	claimReminderSQL = `INSERT INTO reminder_deliveries
		(entity, entity_id, occurrence_at, minutes_before, owner_id, fire_at)
		VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT DO NOTHING`

	markReminderSQL = `UPDATE reminder_deliveries SET push_sent = $5
		WHERE entity = $1 AND entity_id = $2 AND occurrence_at = $3 AND minutes_before = $4`
)

func (s *Store) RemindableEvents(ctx context.Context, from, to time.Time, limit int) ([]domain.Event, error) {
	return listEvents(ctx, s.pool, remindableEventsSQL, from, to, limit)
}

func (s *Store) RemindableTodos(ctx context.Context, from, to time.Time, limit int) ([]domain.Todo, error) {
	return listTodos(ctx, s.pool, remindableTodosSQL, pgDate(from), pgDate(to), limit)
}

func (s *Store) ClaimReminder(ctx context.Context, r domain.Reminder) (bool, error) {
	tag, err := s.pool.Exec(ctx, claimReminderSQL, r.Entity, r.EntityID, r.OccurrenceAt, int32(r.MinutesBefore), r.OwnerID, r.FireAt)
	if err != nil {
		return false, mapErr(err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) MarkReminderSent(ctx context.Context, r domain.Reminder, sent int) error {
	_, err := s.pool.Exec(ctx, markReminderSQL, r.Entity, r.EntityID, r.OccurrenceAt, int32(r.MinutesBefore), int32(sent))
	return mapErr(err)
}
