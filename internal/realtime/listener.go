package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/db"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const channel = "calendar_changes"

type Listener struct {
	url    string
	hub    *Hub
	logger *slog.Logger
}

func NewListener(url string, hub *Hub, logger *slog.Logger) *Listener {
	return &Listener{url: url, hub: hub, logger: logger}
}

func (l *Listener) Run(ctx context.Context) {
	db.Listen(ctx, l.url, channel, l.logger, func(first bool) {
		if !first {
			l.hub.Resync()
		}
	}, func(payload string) {
		c, err := ParsePayload(payload)
		if err != nil {
			l.logger.LogAttrs(ctx, slog.LevelWarn, "bad change notification", slog.Any("err", err))
			return
		}
		l.hub.Publish(c)
	})
}

type payload struct {
	Seq      int64      `json:"seq"`
	Owner    domain.ID  `json:"owner"`
	Entity   string     `json:"entity"`
	ID       domain.ID  `json:"id"`
	Op       string     `json:"op"`
	Calendar *domain.ID `json:"calendar"`
	List     *domain.ID `json:"list"`
	From     *time.Time `json:"from"`
	To       *time.Time `json:"to"`
	Origin   *string    `json:"origin"`
}

func ParsePayload(s string) (domain.Change, error) {
	var p payload
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return domain.Change{}, err
	}
	if p.Seq <= 0 || p.Owner == domain.NilID || p.Entity == "" {
		return domain.Change{}, errors.New("realtime: incomplete change payload")
	}
	c := domain.Change{
		Seq:        p.Seq,
		OwnerID:    p.Owner,
		Entity:     p.Entity,
		EntityID:   p.ID,
		Op:         p.Op,
		CalendarID: p.Calendar,
		ListID:     p.List,
		From:       p.From,
		To:         p.To,
	}
	if p.Origin != nil {
		c.Origin = *p.Origin
	}
	return c, nil
}
