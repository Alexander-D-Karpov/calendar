package domain

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	UndoEventCreate  = "event.create"
	UndoEventRestore = "event.restore"
	UndoEventDelete  = "event.delete"
	UndoTodoCreate   = "todo.create"
	UndoTodoRestore  = "todo.restore"
	UndoTodoDelete   = "todo.delete"
	UndoTodoStatus   = "todo.status"
)

type UndoEntry struct {
	ID        ID
	OwnerID   ID
	Kind      string
	Label     string
	Payload   json.RawMessage
	CreatedAt time.Time
	UsedAt    *time.Time
}

func (e UndoEntry) Entity() string {
	entity, _, _ := strings.Cut(e.Kind, ".")
	return entity
}

func (e UndoEntry) Decode(v any) error {
	return json.Unmarshal(e.Payload, v)
}
