package pg

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/Alexander-D-Karpov/calendar/internal/db"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const (
	sleepSQL       = `SELECT weekday, start_time, end_time FROM sleep_schedule WHERE user_id = $1 ORDER BY weekday`
	clearSleepSQL  = `DELETE FROM sleep_schedule WHERE user_id = $1`
	insertSleepSQL = `INSERT INTO sleep_schedule (user_id, weekday, start_time, end_time) VALUES ($1, $2, $3, $4)`
)

func (s *Store) SleepSchedule(ctx context.Context, user domain.ID) ([]domain.SleepWindow, error) {
	return sleepSchedule(ctx, s.pool, user)
}

func sleepSchedule(ctx context.Context, q db.DBTX, user domain.ID) ([]domain.SleepWindow, error) {
	rows, err := q.Query(ctx, sleepSQL, user)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := []domain.SleepWindow{}
	for rows.Next() {
		var wd int16
		var start, end pgtype.Time
		if err := rows.Scan(&wd, &start, &end); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, domain.SleepWindow{Weekday: time.Weekday(wd), Start: minutes(start), End: minutes(end)})
	}
	return out, mapErr(rows.Err())
}

func replaceSleep(ctx context.Context, q db.DBTX, user domain.ID, ws []domain.SleepWindow) error {
	if _, err := q.Exec(ctx, clearSleepSQL, user); err != nil {
		return err
	}
	for _, w := range ws {
		if _, err := q.Exec(ctx, insertSleepSQL, user, int16(w.Weekday), pgClock(w.Start), pgClock(w.End)); err != nil {
			return err
		}
	}
	return nil
}

func minutes(t pgtype.Time) int {
	return int(t.Microseconds / int64(time.Minute/time.Microsecond))
}

func pgClock(m int) pgtype.Time {
	return pgtype.Time{Microseconds: int64(m) * int64(time.Minute/time.Microsecond), Valid: true}
}
