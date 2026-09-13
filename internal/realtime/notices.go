package realtime

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/Alexander-D-Karpov/calendar/internal/db"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const NoticeChannel = "calendar_notices"

type Notices struct {
	url    string
	hub    *Hub
	logger *slog.Logger
}

func NewNotices(url string, hub *Hub, logger *slog.Logger) *Notices {
	return &Notices{url: url, hub: hub, logger: logger}
}

func (n *Notices) Run(ctx context.Context) {
	db.Listen(ctx, n.url, NoticeChannel, n.logger, func(bool) {}, func(payload string) {
		var msg domain.Notice
		if err := json.Unmarshal([]byte(payload), &msg); err != nil || msg.OwnerID == domain.NilID || msg.Title == "" {
			n.logger.LogAttrs(ctx, slog.LevelWarn, "bad notice payload")
			return
		}
		n.hub.Notify(msg)
	})
}
