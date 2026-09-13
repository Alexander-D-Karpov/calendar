package undo

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const DefaultTTL = 30 * time.Minute

type Repo interface {
	CreateUndo(ctx context.Context, e domain.UndoEntry, expires time.Time) error
	ClaimUndo(ctx context.Context, owner, id domain.ID, at time.Time) (domain.UndoEntry, error)
}

type Options struct {
	Repo   Repo
	Clock  clock.Clock
	Logger *slog.Logger
	TTL    time.Duration
}

type Service struct {
	repo   Repo
	clock  clock.Clock
	logger *slog.Logger
	ttl    time.Duration
}

func New(o Options) *Service {
	if o.Clock == nil {
		o.Clock = clock.New()
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.TTL <= 0 {
		o.TTL = DefaultTTL
	}
	return &Service{repo: o.Repo, clock: o.Clock, logger: o.Logger, ttl: o.TTL}
}

func (s *Service) Record(ctx context.Context, owner domain.ID, kind, label string, payload any) string {
	if s == nil || owner == domain.NilID {
		return ""
	}
	body, err := json.Marshal(payload)
	if err == nil {
		e := domain.UndoEntry{ID: domain.NewID(), OwnerID: owner, Kind: kind, Label: label, Payload: body}
		err = s.repo.CreateUndo(context.WithoutCancel(ctx), e, s.clock.Now().Add(s.ttl))
		if err == nil {
			return e.ID.String()
		}
	}
	s.logger.LogAttrs(ctx, slog.LevelWarn, "undo entry not recorded", slog.String("kind", kind), slog.Any("err", err))
	return ""
}

func (s *Service) Take(ctx context.Context, owner, id domain.ID) (domain.UndoEntry, error) {
	return s.repo.ClaimUndo(ctx, owner, id, s.clock.Now().UTC())
}
