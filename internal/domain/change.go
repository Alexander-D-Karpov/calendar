package domain

import (
	"context"
	"time"
)

const (
	EntityCalendar = "calendar"
	EntityEvent    = "event"
	EntityTodo     = "todo"
	EntityList     = "list"
	EntityShare    = "share"
	EntitySettings = "settings"
)

const (
	OpCreate  = "create"
	OpUpdate  = "update"
	OpDelete  = "delete"
	OpRestore = "restore"
)

type Change struct {
	Seq        int64
	OwnerID    ID
	Entity     string
	EntityID   ID
	Op         string
	CalendarID *ID
	ListID     *ID
	From       *time.Time
	To         *time.Time
	Origin     string
}

type originKey struct{}

func WithOrigin(ctx context.Context, origin string) context.Context {
	return context.WithValue(ctx, originKey{}, origin)
}

func OriginFrom(ctx context.Context) string {
	s, _ := ctx.Value(originKey{}).(string)
	return s
}
