package api

import (
	"net/http"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

type todoListJSON struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	Position  int       `json:"position"`
	IsDefault bool      `json:"is_default"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type todoListList struct {
	Items []todoListJSON `json:"items"`
}

func toTodoListJSON(l domain.TodoList) todoListJSON {
	return todoListJSON{
		ID:        l.ID.String(),
		Name:      l.Name,
		Color:     l.Color,
		Position:  l.Position,
		IsDefault: l.IsDefault,
		CreatedAt: l.CreatedAt.UTC(),
		UpdatedAt: l.UpdatedAt.UTC(),
	}
}

func toTodoListList(list []domain.TodoList) todoListList {
	items := make([]todoListJSON, len(list))
	for i, l := range list {
		items[i] = toTodoListJSON(l)
	}
	return todoListList{Items: items}
}

func listTodoLists(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		list, err := d.TodoLists.List(r.Context(), ownerID(r))
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, toTodoListList(list))
		return nil
	}
}

func getTodoList(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		l, err := d.TodoLists.Get(r.Context(), ownerID(r), id)
		if err != nil {
			return err
		}
		writeResource(w, r, http.StatusOK, l.ETag(), toTodoListJSON(l))
		return nil
	}
}

func createTodoList(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		var p domain.TodoListPatch
		if err := decodeJSON(r, &p); err != nil {
			return err
		}
		l, err := d.TodoLists.Create(r.Context(), ownerID(r), p)
		if err != nil {
			return err
		}
		w.Header().Set("Location", Prefix+"/todo-lists/"+l.ID.String())
		writeResource(w, r, http.StatusCreated, l.ETag(), toTodoListJSON(l))
		return nil
	}
}

func updateTodoList(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		var p domain.TodoListPatch
		if err := decodeJSON(r, &p, mediaMergePatch, mediaJSON); err != nil {
			return err
		}
		l, err := d.TodoLists.Update(r.Context(), ownerID(r), id, p, r.Header.Get("If-Match"))
		if err != nil {
			return err
		}
		writeResource(w, r, http.StatusOK, l.ETag(), toTodoListJSON(l))
		return nil
	}
}

func deleteTodoList(d Deps) apiFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := pathID(r)
		if err != nil {
			return err
		}
		if err := d.TodoLists.Delete(r.Context(), ownerID(r), id, r.Header.Get("If-Match")); err != nil {
			return err
		}
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
}
