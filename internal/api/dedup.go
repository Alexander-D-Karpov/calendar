package api

import (
	"context"
	"net/http"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/dedup"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

type TrashStore interface {
	Trash(ctx context.Context, owner domain.ID, limit int) ([]domain.TrashItem, error)
	Restore(ctx context.Context, owner, id domain.ID, entity string) error
	PurgeItem(ctx context.Context, owner, id domain.ID, entity string) error
}

type duplicateJSON struct {
	ID         string         `json:"id"`
	Entity     string         `json:"entity"`
	Reason     string         `json:"reason"`
	Score      float64        `json:"score"`
	Status     string         `json:"status"`
	A          dedup.Snapshot `json:"a"`
	B          dedup.Snapshot `json:"b"`
	CreatedAt  time.Time      `json:"created_at"`
	ResolvedAt *time.Time     `json:"resolved_at"`
}

type duplicateList struct {
	Items []duplicateJSON `json:"items"`
}

type resolveRequest struct {
	Action string `json:"action"`
}

type trashJSON struct {
	ID        string    `json:"id"`
	Entity    string    `json:"entity"`
	Title     string    `json:"title"`
	When      string    `json:"when"`
	Where     string    `json:"where"`
	Children  int       `json:"children"`
	DeletedAt time.Time `json:"deleted_at"`
}

type trashList struct {
	Items []trashJSON `json:"items"`
}

func listDuplicates(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		pairs, err := d.Dedup.List(r.Context(), ownerID(r), r.URL.Query().Get("status"))
		if err != nil {
			return err
		}
		items := make([]duplicateJSON, len(pairs))
		for i, p := range pairs {
			items[i] = duplicateJSON{
				ID: p.ID.String(), Entity: p.Entity, Reason: p.Reason, Score: p.Score, Status: p.Status,
				A: p.A, B: p.B, CreatedAt: p.CreatedAt.UTC(), ResolvedAt: utcPtr(p.ResolvedAt),
			}
		}
		writeJSON(w, http.StatusOK, duplicateList{Items: items})
		return nil
	}
}

func scanDuplicates(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		if err := d.Dedup.Scan(r.Context(), ownerID(r)); err != nil {
			return err
		}
		w.WriteHeader(http.StatusAccepted)
		return nil
	}
}

func resolveDuplicate(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		var body resolveRequest
		if err := decodeJSON(r, &body); err != nil {
			return err
		}
		if err := d.Dedup.Resolve(r.Context(), ownerID(r), id, body.Action); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}

func listTrash(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		limit, err := queryInt(r.URL.Query(), "limit", 100)
		if err != nil {
			return err
		}
		items, err := d.Store.Trash(r.Context(), ownerID(r), min(max(limit, 1), 500))
		if err != nil {
			return err
		}
		out := make([]trashJSON, len(items))
		for i, it := range items {
			out[i] = trashJSON{
				ID: it.ID.String(), Entity: it.Entity, Title: it.Title, When: it.When, Where: it.Where,
				Children: it.Children, DeletedAt: it.DeletedAt.UTC(),
			}
		}
		writeJSON(w, http.StatusOK, trashList{Items: out})
		return nil
	}
}

func trashTarget(r *http.Request) (domain.ID, string, error) {
	entity := r.PathValue("entity")
	if entity != domain.EntityEvent && entity != domain.EntityTodo {
		return domain.NilID, "", domain.ErrNotFound
	}
	id, err := pathID(r)
	return id, entity, err
}

func restoreTrash(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, entity, err := trashTarget(r)
		if err != nil {
			return err
		}
		if err := d.Store.Restore(r.Context(), ownerID(r), id, entity); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}

func purgeTrash(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, entity, err := trashTarget(r)
		if err != nil {
			return err
		}
		if err := d.Store.PurgeItem(r.Context(), ownerID(r), id, entity); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}
