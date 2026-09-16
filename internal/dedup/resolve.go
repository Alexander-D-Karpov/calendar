package dedup

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/store"
)

const (
	ActionKeepA   = "keep_a"
	ActionKeepB   = "keep_b"
	ActionMerge   = "merge"
	ActionDismiss = "dismiss"
)

var Actions = []string{ActionKeepA, ActionKeepB, ActionMerge, ActionDismiss}

type Pair struct {
	domain.Duplicate
	A Snapshot
	B Snapshot
}

func (s *Service) List(ctx context.Context, owner domain.ID, status string) ([]Pair, error) {
	if status == "" {
		status = domain.DupPending
	}
	rows, err := s.repo.Duplicates(ctx, owner, status, reviewLimit)
	if err != nil {
		return nil, err
	}
	out := make([]Pair, len(rows))
	for i, d := range rows {
		out[i] = Pair{Duplicate: d, A: Decode(d.A), B: Decode(d.B)}
	}
	return out, nil
}

func (s *Service) Count(ctx context.Context, owner domain.ID) (int, error) {
	return s.repo.CountDuplicates(ctx, owner)
}

func (s *Service) Resolve(ctx context.Context, owner, id domain.ID, action string) error {
	if !slices.Contains(Actions, action) {
		var v domain.ValidationError
		v.Add("action", "must be keep_a, keep_b, merge or dismiss")
		return v.Err()
	}
	ctx = domain.WithOrigin(ctx, domain.OriginDedup)
	return s.repo.WithDedup(ctx, owner, func(tx store.DedupTx) error {
		d, err := tx.Duplicate(ctx, owner, id)
		if err != nil {
			return err
		}
		if !d.Pending() {
			return fmt.Errorf("%w: this pair was already handled", domain.ErrConflict)
		}
		now := s.now()
		switch action {
		case ActionDismiss:
			return tx.ResolveDuplicate(ctx, owner, id, domain.DupDismissed, now)
		case ActionMerge:
			if err := s.merge(ctx, tx, owner, d); err != nil {
				return err
			}
			return tx.ResolveDuplicate(ctx, owner, id, domain.DupMerged, now)
		}
		drop := d.BID
		if action == ActionKeepB {
			drop = d.AID
		}
		if err := s.remove(ctx, tx, owner, d, drop); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		// Deleting the event drops every pair that referenced it, this one
		// included, so there is usually no row left to mark. That is the
		// expected end state, not a missing pair: reporting it as an error
		// rolled the whole transaction back, restoring the event and leaving
		// the pair pending, so this path could never resolve anything.
		if err := tx.ResolveDuplicate(ctx, owner, id, domain.DupDeleted, now); err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		return nil
	})
}

func (s *Service) remove(ctx context.Context, tx store.DedupTx, owner domain.ID, d domain.Duplicate, id domain.ID) error {
	now := s.now()
	if d.Entity == domain.EntityTodo {
		t, err := tx.Todos().Get(ctx, id)
		if err != nil {
			return err
		}
		if err := tx.Todos().Delete(ctx, t, now); err != nil {
			return err
		}
		return tx.DropDuplicates(ctx, owner, domain.EntityTodo, id)
	}
	e, err := tx.Events().Get(ctx, id)
	if err != nil {
		return err
	}
	if err := tx.Events().Delete(ctx, e, now); err != nil {
		return err
	}
	return tx.DropDuplicates(ctx, owner, domain.EntityEvent, id)
}

func (s *Service) merge(ctx context.Context, tx store.DedupTx, owner domain.ID, d domain.Duplicate) error {
	if d.Entity == domain.EntityTodo {
		return s.mergeTodos(ctx, tx, owner, d)
	}
	keep, err := tx.Events().Get(ctx, d.AID)
	if err != nil {
		return err
	}
	drop, err := tx.Events().Get(ctx, d.BID)
	if err != nil {
		return err
	}
	if keep.Title == "" {
		keep.Title = drop.Title
	}
	if keep.Body == "" {
		keep.Body = drop.Body
	}
	if keep.Location == "" {
		keep.Location = drop.Location
	}
	if keep.URL == "" {
		keep.URL = drop.URL
	}
	if len(keep.Reminders) == 0 {
		keep.Reminders = drop.Reminders
	}
	if keep.RRule == "" && drop.RRule != "" && keep.Start.Equal(drop.Start) {
		keep.RRule, keep.RDate, keep.ExDate = drop.RRule, drop.RDate, drop.ExDate
	}
	if _, err := tx.Events().Update(ctx, keep); err != nil {
		return err
	}
	return s.remove(ctx, tx, owner, d, drop.ID)
}

func (s *Service) mergeTodos(ctx context.Context, tx store.DedupTx, owner domain.ID, d domain.Duplicate) error {
	keep, err := tx.Todos().Get(ctx, d.AID)
	if err != nil {
		return err
	}
	drop, err := tx.Todos().Get(ctx, d.BID)
	if err != nil {
		return err
	}
	if keep.Body == "" {
		keep.Body = drop.Body
	}
	if keep.Priority == 0 {
		keep.Priority = drop.Priority
	}
	if keep.DueTime == nil && drop.DueTime != nil {
		keep.DueTime, keep.Duration, keep.TZ = drop.DueTime, drop.Duration, drop.TZ
	}
	if len(keep.Reminders) == 0 {
		keep.Reminders = drop.Reminders
	}
	saved, err := tx.Todos().Update(ctx, keep)
	if err != nil {
		return err
	}
	have := make(map[string]bool, len(saved.Checks))
	for _, c := range saved.Checks {
		have[c.Text] = true
	}
	prev := ""
	if n := len(saved.Checks); n > 0 {
		prev = saved.Checks[n-1].Position
	}
	for _, c := range drop.Checks {
		if have[c.Text] || len(saved.Checks) >= domain.MaxChecks {
			continue
		}
		c.ID, c.TodoID, c.Position = domain.NewID(), saved.ID, domain.KeyBetween(prev, "")
		prev = c.Position
		added, err := tx.Todos().InsertCheck(ctx, c)
		if err != nil {
			return err
		}
		saved.Checks = append(saved.Checks, added)
		have[c.Text] = true
	}
	if _, err := tx.Todos().Touch(ctx, saved); err != nil {
		return err
	}
	return s.remove(ctx, tx, owner, d, drop.ID)
}
