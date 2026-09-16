package store

import (
	"context"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

type EventTx interface {
	Get(ctx context.Context, id domain.ID) (domain.Event, error)
	Override(ctx context.Context, series domain.ID, at time.Time) (domain.Event, error)
	Overrides(ctx context.Context, series domain.ID) ([]domain.Event, error)
	Insert(ctx context.Context, e domain.Event) (domain.Event, error)
	Update(ctx context.Context, e domain.Event) (domain.Event, error)
	Delete(ctx context.Context, e domain.Event, at time.Time) error
	MoveOverrides(ctx context.Context, from domain.ID, to domain.Event, since time.Time) error
}

type TodoOrder int

const (
	OrderByID TodoOrder = iota
	OrderByPosition
	OrderByDue
	OrderByCompleted
)

type TodoFilter struct {
	Lists      []domain.ID
	Parent     *domain.ID
	TopLevel   bool
	Status     string
	Scheduled  bool
	OnCalendar bool
	DueFrom    *time.Time
	DueTo      *time.Time
	After      *domain.ID
	Order      TodoOrder
	Limit      int
}

type TodoTx interface {
	Get(ctx context.Context, id domain.ID) (domain.Todo, error)
	Insert(ctx context.Context, t domain.Todo) (domain.Todo, error)
	Update(ctx context.Context, t domain.Todo) (domain.Todo, error)
	Delete(ctx context.Context, t domain.Todo, at time.Time) error
	HasChildren(ctx context.Context, id domain.ID) (bool, error)
	LastPosition(ctx context.Context, list domain.ID, parent *domain.ID, exclude domain.ID) (string, error)
	NextPosition(ctx context.Context, list domain.ID, parent *domain.ID, after string, exclude domain.ID) (string, error)
	InsertCheck(ctx context.Context, c domain.Check) (domain.Check, error)
	UpdateCheck(ctx context.Context, c domain.Check) (domain.Check, error)
	DeleteCheck(ctx context.Context, todo, id domain.ID) error
	Touch(ctx context.Context, t domain.Todo) (domain.Todo, error)
}

type SyncTx interface {
	Events() EventTx
	Todos() TodoTx
	Mapping(ctx context.Context, entity string, local domain.ID) (domain.SyncMapping, error)
	MappingByRemote(ctx context.Context, entity, remote string) (domain.SyncMapping, error)
	Mappings(ctx context.Context, entity string) ([]domain.SyncMapping, error)
	SaveMapping(ctx context.Context, m domain.SyncMapping) error
	DeleteMapping(ctx context.Context, entity string, local domain.ID) error
	DeleteMappingsWithPrefix(ctx context.Context, entity, prefix string) error
	UIDTaken(ctx context.Context, calendar domain.ID, uid string) (bool, error)
	EventByUID(ctx context.Context, calendar domain.ID, uid string) (domain.Event, error)
	ClaimOutbox(ctx context.Context, id int64) (domain.OutboxItem, bool, error)
	FinishOutbox(ctx context.Context, id int64) error
	RetryOutbox(ctx context.Context, id int64, attempts int, next time.Time, lastErr string) error
	DropOutbox(ctx context.Context, entity string, local domain.ID) error
	PendingOp(ctx context.Context, entity string, local domain.ID) (string, bool, error)
	Conflict(ctx context.Context, c domain.SyncConflict) error
}

type ImportTx interface {
	Events() EventTx
	Todos() TodoTx
	CreateCalendar(ctx context.Context, c domain.Calendar) (domain.Calendar, error)
	CreateTodoList(ctx context.Context, l domain.TodoList) (domain.TodoList, error)
	CalendarEvents(ctx context.Context, calendar domain.ID) ([]domain.Event, error)
	EventByUID(ctx context.Context, calendar domain.ID, uid string) (domain.Event, error)
	EventFingerprints(ctx context.Context, calendar domain.ID, fps [][]byte) (map[string]bool, error)
	TodoByUID(ctx context.Context, list domain.ID, uid string) (domain.Todo, error)
	TodoFingerprints(ctx context.Context, list domain.ID, fps [][]byte) (map[string]bool, error)
}

type DedupTx interface {
	Events() EventTx
	Todos() TodoTx
	Duplicate(ctx context.Context, owner, id domain.ID) (domain.Duplicate, error)
	SaveDuplicate(ctx context.Context, d domain.Duplicate) error
	ResolveDuplicate(ctx context.Context, owner, id domain.ID, status string, at time.Time) error
	DropDuplicates(ctx context.Context, owner domain.ID, entity string, ids ...domain.ID) error
}
