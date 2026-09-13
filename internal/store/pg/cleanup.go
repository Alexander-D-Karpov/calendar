package pg

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

const purgeBatch = 5000

type Cutoffs struct {
	Now     time.Time
	Trash   time.Time
	Changes time.Time
	Audit   time.Time
	Jobs    time.Time
}

type purgeStep struct {
	table string
	where string
	at    time.Time
}

func (s *Store) Purge(ctx context.Context, c Cutoffs) (map[string]int64, error) {
	steps := []purgeStep{
		{"sessions", "expires_at < $1 OR absolute_expires_at < $1", c.Now},
		{"email_tokens", "expires_at < $1 OR used_at IS NOT NULL", c.Now},
		{"idempotency_keys", "created_at < $1", c.Now.Add(-24 * time.Hour)},
		{"undo_entries", "expires_at < $1 OR used_at IS NOT NULL", c.Now},
		{"imports", "expires_at < $1", c.Now},
		{"events", "deleted_at < $1", c.Trash},
		{"todos", "deleted_at < $1", c.Trash},
		{"reminder_deliveries", "delivered_at < $1", c.Trash},
		{"changes", "created_at < $1", c.Changes},
		{"audit_log", "created_at < $1", c.Audit},
		{"api_tokens", "revoked_at < $1 OR expires_at < $1", c.Audit},
		{"jobs", "status = 'failed' AND updated_at < $1", c.Jobs},
	}
	out := make(map[string]int64, len(steps))
	for _, st := range steps {
		sql := `DELETE FROM ` + st.table + ` WHERE ctid = ANY(ARRAY(SELECT ctid FROM ` + st.table +
			` WHERE ` + st.where + ` LIMIT ` + strconv.Itoa(purgeBatch) + `))`
		for {
			tag, err := s.pool.Exec(ctx, sql, st.at)
			if err != nil {
				return out, fmt.Errorf("purge %s: %w", st.table, err)
			}
			out[st.table] += tag.RowsAffected()
			if tag.RowsAffected() < purgeBatch {
				break
			}
		}
	}
	return out, nil
}
