package maintenance

import (
	"context"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

type Store interface {
	Trash(ctx context.Context, owner domain.ID, limit int) ([]domain.TrashItem, error)
	Restore(ctx context.Context, owner, id domain.ID, entity string) error
	PurgeItem(ctx context.Context, owner, id domain.ID, entity string) error
}
