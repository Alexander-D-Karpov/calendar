package gsync

import (
	"context"
	"errors"
	"log/slog"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/google"
	"github.com/Alexander-D-Karpov/calendar/internal/jobs"
	"github.com/Alexander-D-Karpov/calendar/internal/store"
)

func (e *Engine) HandlePush(ctx context.Context, j jobs.Job) error {
	var p BindingJob
	if err := j.Decode(&p); err != nil {
		return jobs.Permanent(err)
	}
	b, acct, ok, err := e.load(ctx, p.Binding)
	if err != nil || !ok || !b.Pushes() {
		return err
	}
	r, err := e.remote(ctx, acct)
	if err != nil {
		return err
	}
	ctx = domain.WithOrigin(ctx, domain.OriginGoogle)
	ids, err := e.store.PendingOutbox(ctx, b.ID, pushBatch)
	if err != nil {
		return err
	}
	sent := 0
	for _, id := range ids {
		err := e.store.WithSync(ctx, b.OwnerID, b.ID, func(tx store.SyncTx) error {
			it, ok, err := tx.ClaimOutbox(ctx, id)
			if err != nil || !ok {
				return err
			}
			perr := e.pushItem(ctx, tx, r, b, it)
			attempt := it.Attempts + 1
			attrs := []slog.Attr{
				slog.String("binding", b.ID.String()), slog.String("entity", it.Entity),
				slog.String("local", it.LocalID.String()), slog.Int("attempt", attempt), slog.Any("err", perr),
			}
			switch {
			case perr == nil:
				sent++
				return tx.FinishOutbox(ctx, it.ID)
			case errors.Is(perr, google.ErrRevoked):
				return perr
			case attempt >= maxPushTries:
				e.logger.LogAttrs(ctx, slog.LevelError, "google push dropped", attrs...)
				return tx.FinishOutbox(ctx, it.ID)
			}
			e.logger.LogAttrs(ctx, slog.LevelWarn, "google push failed", attrs...)
			return tx.RetryOutbox(ctx, it.ID, attempt, e.now().Add(retryDelay(attempt)), scrub(perr))
		})
		if err != nil {
			e.observe(b.Entity, "push", sent, err)
			return e.failed(ctx, acct, err)
		}
	}
	e.observe(b.Entity, "push", sent, nil)
	return e.queuePush(ctx, b)
}

func (e *Engine) pushItem(ctx context.Context, tx store.SyncTx, r google.Remote, b domain.SyncBinding, it domain.OutboxItem) error {
	if it.Entity == domain.EntityTodo {
		return e.pushTodo(ctx, tx, r, b, it)
	}
	return e.pushEvent(ctx, tx, r, b, it)
}

func saveEventMapping(ctx context.Context, tx store.SyncTx, local domain.Event, out google.Event) error {
	return tx.SaveMapping(ctx, domain.SyncMapping{
		Entity: domain.EntityEvent, LocalID: local.ID, RemoteID: out.ID, RemoteETag: out.ETag,
		RemoteUpdated: timePtr(out.Updated), LocalVersion: local.Version,
	})
}

func (e *Engine) pushEvent(ctx context.Context, tx store.SyncTx, r google.Remote, b domain.SyncBinding, it domain.OutboxItem) error {
	m, mapped, err := byLocal(ctx, tx, domain.EntityEvent, it.LocalID)
	if err != nil {
		return err
	}
	remoteID := it.RemoteID
	if mapped {
		remoteID = m.RemoteID
	}
	var local domain.Event
	if it.Op == domain.SyncUpsert {
		local, err = tx.Events().Get(ctx, it.LocalID)
		switch {
		case errors.Is(err, domain.ErrNotFound), err == nil && (b.CalendarID == nil || local.CalendarID != *b.CalendarID):
			it.Op = domain.SyncDelete
		case err != nil:
			return err
		}
	}
	if it.Op == domain.SyncDelete {
		if remoteID == "" {
			return nil
		}
		if err := r.DeleteEvent(ctx, b.RemoteID, remoteID); err != nil && !gone(err) {
			return err
		}
		if err := tx.DeleteMapping(ctx, domain.EntityEvent, it.LocalID); err != nil {
			return err
		}
		return tx.DeleteMappingsWithPrefix(ctx, domain.EntityEvent, remoteID+"_")
	}
	if local.ReadOnly {
		return nil
	}
	instance := local.IsOverride()
	if instance && !mapped {
		mm, ok, err := byLocal(ctx, tx, domain.EntityEvent, *local.SeriesID)
		if err != nil {
			return err
		}
		if !ok {
			return errMasterPending
		}
		inst, err := r.Instance(ctx, b.RemoteID, mm.RemoteID, instanceStart(local))
		if gone(err) {
			return errMasterPending
		}
		if err != nil {
			return err
		}
		remoteID = inst.ID
		m = domain.SyncMapping{Entity: domain.EntityEvent, LocalID: local.ID, RemoteID: inst.ID, RemoteETag: inst.ETag}
	}
	body := toRemoteEvent(local, instance)
	var out google.Event
	if remoteID == "" {
		out, err = r.InsertEvent(ctx, b.RemoteID, body)
	} else {
		body.ID = remoteID
		out, err = r.PatchEvent(ctx, b.RemoteID, body, m.RemoteETag)
		switch {
		case errors.Is(err, google.ErrPrecondition):
			return e.resolveEvent(ctx, tx, r, b, local, body, m)
		case gone(err) && instance:
			return tx.DeleteMapping(ctx, domain.EntityEvent, local.ID)
		case gone(err):
			body.ID = ""
			out, err = r.InsertEvent(ctx, b.RemoteID, body)
		}
	}
	if err != nil {
		return err
	}
	return saveEventMapping(ctx, tx, local, out)
}

func (e *Engine) resolveEvent(ctx context.Context, tx store.SyncTx, r google.Remote, b domain.SyncBinding, local domain.Event, body google.Event, m domain.SyncMapping) error {
	cur, err := r.Event(ctx, b.RemoteID, body.ID)
	if err != nil {
		return err
	}
	if !local.UpdatedAt.After(cur.Updated) {
		p, err := e.eventPuller(ctx, tx, b)
		if err != nil {
			return err
		}
		m.LocalID, m.RemoteID = local.ID, body.ID
		return p.update(ctx, m, cur, func() error { return nil })
	}
	err = tx.Conflict(ctx, domain.SyncConflict{
		OwnerID: b.OwnerID, BindingID: b.ID, Entity: domain.EntityEvent, LocalID: local.ID, RemoteID: body.ID,
		Winner: domain.WinnerLocal, Local: local, Remote: cur,
	})
	if err != nil {
		return err
	}
	out, err := r.PatchEvent(ctx, b.RemoteID, body, "")
	if err != nil {
		return err
	}
	return saveEventMapping(ctx, tx, local, out)
}

func (e *Engine) pushTodo(ctx context.Context, tx store.SyncTx, r google.Remote, b domain.SyncBinding, it domain.OutboxItem) error {
	m, mapped, err := byLocal(ctx, tx, domain.EntityTodo, it.LocalID)
	if err != nil {
		return err
	}
	remoteID := it.RemoteID
	if mapped {
		remoteID = m.RemoteID
	}
	var local domain.Todo
	if it.Op == domain.SyncUpsert {
		local, err = tx.Todos().Get(ctx, it.LocalID)
		switch {
		case errors.Is(err, domain.ErrNotFound), err == nil && (b.ListID == nil || local.ListID != *b.ListID):
			it.Op = domain.SyncDelete
		case err != nil:
			return err
		}
	}
	if it.Op == domain.SyncDelete {
		if remoteID != "" {
			if err := r.DeleteTask(ctx, b.RemoteID, remoteID); err != nil && !gone(err) {
				return err
			}
		}
		return tx.DeleteMapping(ctx, domain.EntityTodo, it.LocalID)
	}
	parent := ""
	if local.ParentID != nil {
		pm, ok, err := byLocal(ctx, tx, domain.EntityTodo, *local.ParentID)
		if err != nil {
			return err
		}
		if !ok {
			return errParentPending
		}
		parent = pm.RemoteID
	}
	body := taskFromTodo(local, parent)
	var out google.Task
	if remoteID != "" {
		body.ID = remoteID
		out, err = r.PatchTask(ctx, b.RemoteID, body)
		if gone(err) {
			body.ID, remoteID = "", ""
		}
	}
	if remoteID == "" {
		out, err = r.InsertTask(ctx, b.RemoteID, body)
	}
	if err == nil && out.Parent != parent {
		out, err = r.MoveTask(ctx, b.RemoteID, out.ID, parent)
	}
	if err != nil {
		return err
	}
	return tx.SaveMapping(ctx, domain.SyncMapping{
		Entity: domain.EntityTodo, LocalID: local.ID, RemoteID: out.ID, RemoteETag: out.ETag,
		RemoteUpdated: timePtr(out.Updated), LocalVersion: local.Version, RemoteDueDate: local.DueDate,
	})
}
