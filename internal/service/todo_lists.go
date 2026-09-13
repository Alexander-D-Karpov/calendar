package service

import (
	"context"
	"fmt"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

type TodoListRepo interface {
	ListTodoLists(ctx context.Context, owner domain.ID) ([]domain.TodoList, error)
	CountTodoLists(ctx context.Context, owner domain.ID) (int, error)
	TodoList(ctx context.Context, owner, id domain.ID) (domain.TodoList, error)
	CreateTodoList(ctx context.Context, l domain.TodoList) (domain.TodoList, error)
	UpdateTodoList(ctx context.Context, owner, id domain.ID, fn func(*domain.TodoList) error) (domain.TodoList, error)
	DeleteTodoList(ctx context.Context, owner, id domain.ID, fn func(domain.TodoList) error) error
}

type TodoLists struct {
	repo TodoListRepo
}

func NewTodoLists(repo TodoListRepo) *TodoLists {
	return &TodoLists{repo: repo}
}

func (s *TodoLists) List(ctx context.Context, owner domain.ID) ([]domain.TodoList, error) {
	return s.repo.ListTodoLists(ctx, owner)
}

func (s *TodoLists) Get(ctx context.Context, owner, id domain.ID) (domain.TodoList, error) {
	return s.repo.TodoList(ctx, owner, id)
}

func (s *TodoLists) Create(ctx context.Context, owner domain.ID, p domain.TodoListPatch) (domain.TodoList, error) {
	n, err := s.repo.CountTodoLists(ctx, owner)
	if err != nil {
		return domain.TodoList{}, err
	}
	if n >= domain.MaxTodoLists {
		return domain.TodoList{}, fmt.Errorf("%w: an account can have at most %d lists", domain.ErrConflict, domain.MaxTodoLists)
	}
	l := domain.TodoList{ID: domain.NewID(), OwnerID: owner, Color: domain.DefaultListColor, Position: -1}
	if err := applyList(&l, p); err != nil {
		return domain.TodoList{}, err
	}
	return s.repo.CreateTodoList(ctx, l)
}

func (s *TodoLists) Update(ctx context.Context, owner, id domain.ID, p domain.TodoListPatch, ifMatch string) (domain.TodoList, error) {
	return s.repo.UpdateTodoList(ctx, owner, id, func(l *domain.TodoList) error {
		if err := domain.CheckIfMatch(ifMatch, l.ETag()); err != nil {
			return err
		}
		return applyList(l, p)
	})
}

func (s *TodoLists) Delete(ctx context.Context, owner, id domain.ID, ifMatch string) error {
	return s.repo.DeleteTodoList(ctx, owner, id, func(l domain.TodoList) error {
		if l.IsDefault {
			return fmt.Errorf("%w: the default list cannot be deleted", domain.ErrConflict)
		}
		return domain.CheckIfMatch(ifMatch, l.ETag())
	})
}

func applyList(l *domain.TodoList, p domain.TodoListPatch) error {
	var v domain.ValidationError
	applyNamed(&v, p.Name, p.Color, p.Position, &l.Name, &l.Color, &l.Position)
	v.Length("name", l.Name, 1, 100)
	return v.Err()
}
