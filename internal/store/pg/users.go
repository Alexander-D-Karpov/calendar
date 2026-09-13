package pg

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Alexander-D-Karpov/calendar/internal/db/sqlc"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const (
	defaultCalendarName = "Calendar"
	defaultListName     = "Inbox"
)

func (s *Store) CreateUser(ctx context.Context, nu domain.NewUser) (domain.User, error) {
	var out domain.User
	err := s.rawTx(ctx, func(tx pgx.Tx, q *sqlc.Queries) error {
		u, err := q.CreateUser(ctx, sqlc.CreateUserParams{
			ID:              nu.ID,
			Email:           nu.Email,
			EmailVerifiedAt: nu.EmailVerifiedAt,
			PasswordHash:    textPtr(nu.PasswordHash),
			DisplayName:     nu.DisplayName,
			Timezone:        nu.Timezone,
		})
		if err != nil {
			return err
		}
		if err := q.CreateDefaultCalendar(ctx, sqlc.CreateDefaultCalendarParams{
			ID: domain.NewID(), OwnerID: u.ID, Name: defaultCalendarName,
		}); err != nil {
			return err
		}
		if err := q.CreateDefaultTodoList(ctx, sqlc.CreateDefaultTodoListParams{
			ID: domain.NewID(), OwnerID: u.ID, Name: defaultListName,
		}); err != nil {
			return err
		}
		if nu.Identity != nil {
			id := *nu.Identity
			id.UserID = u.ID
			if err := insertIdentity(ctx, tx, id); err != nil {
				return err
			}
		}
		out = toUser(u)
		return nil
	})
	return out, mapErr(err)
}

func (s *Store) UserByID(ctx context.Context, id domain.ID) (domain.User, error) {
	u, err := s.q.GetUserByID(ctx, id)
	if err != nil {
		return domain.User{}, mapErr(err)
	}
	return toUser(u), nil
}

func (s *Store) UserByEmail(ctx context.Context, email string) (domain.User, error) {
	u, err := s.q.GetUserByEmail(ctx, email)
	if err != nil {
		return domain.User{}, mapErr(err)
	}
	return toUser(u), nil
}

func (s *Store) ListUsers(ctx context.Context) ([]domain.User, error) {
	rows, err := s.q.ListUsers(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]domain.User, len(rows))
	for i, u := range rows {
		out[i] = toUser(u)
	}
	return out, nil
}

func (s *Store) SetPasswordHash(ctx context.Context, id domain.ID, hash string) error {
	return affected(s.q.SetUserPassword(ctx, sqlc.SetUserPasswordParams{ID: id, PasswordHash: textPtr(hash)}))
}

func (s *Store) SetUserDisabled(ctx context.Context, id domain.ID, at *time.Time) error {
	return affected(s.q.SetUserDisabled(ctx, sqlc.SetUserDisabledParams{ID: id, DisabledAt: at}))
}

func toUser(u sqlc.User) domain.User {
	return domain.User{
		ID:               u.ID,
		Email:            u.Email,
		EmailVerifiedAt:  u.EmailVerifiedAt,
		PasswordHash:     text(u.PasswordHash),
		DisplayName:      u.DisplayName,
		Timezone:         u.Timezone,
		WeekStart:        int(u.WeekStart),
		TimeFormat:       u.TimeFormat,
		DefaultView:      u.DefaultView,
		SleepEnabled:     u.SleepEnabled,
		DedupPolicy:      u.DedupPolicy,
		DateOnlyReminder: clockMinutes(u.DateOnlyReminderTime),
		CreatedAt:        u.CreatedAt,
		UpdatedAt:        u.UpdatedAt,
		DisabledAt:       u.DisabledAt,
	}
}
