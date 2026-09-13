package todos

import (
	"context"
	"net/url"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

type todoRef struct {
	ID string `json:"id"`
}

type todoSnapshot struct {
	ID     string     `json:"id"`
	Values url.Values `json:"values"`
	List   bool       `json:"list"`
}

type todoStatus struct {
	ID   string `json:"id"`
	Done bool   `json:"done"`
}

func (m *Module) ApplyUndo(ctx context.Context, owner domain.ID, entry domain.UndoEntry) error {
	switch entry.Kind {
	case domain.UndoTodoCreate:
		id, err := decodeRef(entry)
		if err != nil {
			return err
		}
		return m.todos.Delete(ctx, owner, id, "")
	case domain.UndoTodoDelete:
		id, err := decodeRef(entry)
		if err != nil {
			return err
		}
		return m.store.Restore(ctx, owner, id, domain.EntityTodo)
	case domain.UndoTodoStatus:
		var p todoStatus
		if err := entry.Decode(&p); err != nil {
			return domain.ErrInvalid
		}
		id, err := domain.ParseID(p.ID)
		if err != nil {
			return domain.ErrNotFound
		}
		if p.Done {
			_, _, err = m.todos.Complete(ctx, owner, id, "")
			return err
		}
		_, err = m.todos.Reopen(ctx, owner, id, "")
		return err
	case domain.UndoTodoRestore:
		var p todoSnapshot
		if err := entry.Decode(&p); err != nil {
			return domain.ErrInvalid
		}
		id, err := domain.ParseID(p.ID)
		if err != nil {
			return domain.ErrNotFound
		}
		patch, err := todoPatch(web.NewForm(p.Values), p.List)
		if err != nil {
			return err
		}
		_, err = m.todos.Update(ctx, owner, id, patch, "")
		return err
	}
	return domain.ErrNotFound
}

func decodeRef(entry domain.UndoEntry) (domain.ID, error) {
	var p todoRef
	if err := entry.Decode(&p); err != nil {
		return domain.NilID, domain.ErrInvalid
	}
	id, err := domain.ParseID(p.ID)
	if err != nil {
		return domain.NilID, domain.ErrNotFound
	}
	return id, nil
}
