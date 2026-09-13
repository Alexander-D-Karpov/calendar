package pg

import (
	"context"
	"encoding/json"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/realtime"
)

const notifyUserSQL = `SELECT pg_notify($1, $2)`

func (s *Store) NotifyUser(ctx context.Context, n domain.Notice) error {
	body, err := json.Marshal(n)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, notifyUserSQL, realtime.NoticeChannel, string(body))
	return mapErr(err)
}
