//go:build integration

package pg

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/service"
	"github.com/Alexander-D-Karpov/calendar/internal/testutil/pgtest"
)

func TestTodoService(t *testing.T) {
	ctx := context.Background()
	st := New(pgtest.New(t).Pool)
	u, err := st.CreateUser(ctx, domain.NewUser{ID: domain.NewID(), Email: "todo@example.com", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	todos := service.NewTodos(st, st, clock.NewFake(time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)))
	lists := service.NewTodoLists(st)
	all, err := lists.List(ctx, u.ID)
	if err != nil || len(all) != 1 || !all[0].IsDefault {
		t.Fatalf("lists = %v %v", all, err)
	}
	inbox := all[0]
	work, err := lists.Create(ctx, u.ID, domain.TodoListPatch{Name: domain.Some("Work")})
	if err != nil {
		t.Fatal(err)
	}

	parent, err := todos.Create(ctx, u.ID, domain.TodoPatch{
		Title: domain.Some("Assemble GPU node"), DueDate: domain.Some("2026-09-12"), RRule: domain.Some("FREQ=WEEKLY;COUNT=2"),
		Checks: domain.Some([]domain.CheckInput{{Text: "Riser cables", Done: true}, {Text: "Thermal paste"}}),
	})
	if err != nil || parent.ListID != inbox.ID || len(parent.Checks) != 2 || !parent.Checks[0].Done {
		t.Fatalf("parent = %+v %v", parent, err)
	}
	child, err := todos.Create(ctx, u.ID, domain.TodoPatch{Title: domain.Some("Order fans"), ParentID: domain.Some(parent.ID.String())})
	if err != nil || child.ListID != inbox.ID || child.ParentID == nil {
		t.Fatalf("child = %+v %v", child, err)
	}
	if _, err := todos.Create(ctx, u.ID, domain.TodoPatch{Title: domain.Some("x"), ParentID: domain.Some(child.ID.String())}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("grandchild err = %v", err)
	}

	if _, err := todos.Move(ctx, u.ID, parent.ID, domain.TodoMove{ListID: domain.Some(work.ID.String())}, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := todos.Get(ctx, u.ID, child.ID); got.ListID != work.ID {
		t.Fatalf("child list = %s", got.ListID)
	}
	if _, err := todos.Update(ctx, u.ID, child.ID, domain.TodoPatch{ListID: domain.Some(inbox.ID.String())}, ""); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("move subtask alone err = %v", err)
	}

	done, next, err := todos.Complete(ctx, u.ID, parent.ID, "")
	if err != nil || !done.Done() || done.RRule != "" || next == nil {
		t.Fatalf("complete = %+v %v %v", done, next, err)
	}
	if next.DueDate.Format(domain.DateLayout) != "2026-09-19" || !strings.Contains(next.RRule, "COUNT=1") || len(next.Checks) != 2 || next.Checks[0].Done {
		t.Fatalf("next = %+v", next)
	}
	if _, again, _ := todos.Complete(ctx, u.ID, next.ID, ""); again != nil {
		t.Fatal("COUNT=1 must not repeat")
	}

	c, err := todos.AddCheck(ctx, u.ID, next.ID, domain.CheckPatch{Text: domain.Some("Fans")})
	if err != nil {
		t.Fatal(err)
	}
	cur, _ := todos.Get(ctx, u.ID, next.ID)
	order := []string{c.ID.String(), cur.Checks[0].ID.String(), cur.Checks[1].ID.String()}
	checks, err := todos.ReorderChecks(ctx, u.ID, next.ID, order)
	if err != nil || checks[0].ID != c.ID {
		t.Fatalf("reorder = %v %v", checks, err)
	}
	if after, _ := todos.Get(ctx, u.ID, next.ID); after.Version <= cur.Version || after.Checks[0].ID != c.ID {
		t.Fatalf("after reorder = %+v", after)
	}

	if err := todos.Delete(ctx, u.ID, parent.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := todos.Get(ctx, u.ID, child.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("child after parent delete err = %v", err)
	}
	if err := lists.Delete(ctx, u.ID, inbox.ID, ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("default list delete err = %v", err)
	}
}
