package pg

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/Alexander-D-Karpov/calendar/internal/db/sqlc"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const (
	lockUserSQL       = `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`
	updateSettingsSQL = `UPDATE users SET display_name = $2, timezone = $3, week_start = $4, time_format = $5,
		default_view = $6, sleep_enabled = $7, dedup_policy = $8, date_only_reminder_time = $9, updated_at = now() WHERE id = $1 RETURNING updated_at`
)

func (s *Store) UpdateSettings(ctx context.Context, id domain.ID, fn func(*domain.User, *[]domain.SleepWindow) error) (domain.User, []domain.SleepWindow, error) {
	var (
		out domain.User
		ws  []domain.SleepWindow
	)
	err := s.rawTx(ctx, func(tx pgx.Tx, q *sqlc.Queries) error {
		if _, err := tx.Exec(ctx, lockUserSQL, id); err != nil {
			return err
		}
		row, err := q.GetUserByID(ctx, id)
		if err != nil {
			return err
		}
		u := toUser(row)
		cur, err := sleepSchedule(ctx, tx, id)
		if err != nil {
			return err
		}
		next := slices.Clone(cur)
		if err := fn(&u, &next); err != nil {
			return err
		}
		err = tx.QueryRow(ctx, updateSettingsSQL, id, u.DisplayName, u.Timezone, int16(u.WeekStart), u.TimeFormat,
			u.DefaultView, u.SleepEnabled, u.DedupPolicy, pgClock(u.DateOnlyReminder)).Scan(&u.UpdatedAt)
		if err != nil {
			return err
		}
		if !slices.Equal(cur, next) {
			if err := replaceSleep(ctx, tx, id, next); err != nil {
				return err
			}
		}
		out, ws = u, next
		return recordChange(ctx, q, domain.Change{OwnerID: id, Entity: domain.EntitySettings, EntityID: id, Op: domain.OpUpdate})
	})
	return out, ws, mapErr(err)
}
