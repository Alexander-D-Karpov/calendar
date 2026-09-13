package search

import (
	"context"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/markdown"
	"github.com/Alexander-D-Karpov/calendar/internal/view"
)

const (
	DefaultLimit = 25
	MaxLimit     = 100
	typeNone     = "none"
	snippetRunes = 160
)

type Hit struct {
	Kind      string
	ID        domain.ID
	Title     string
	Snippet   string
	Container string
	Color     string
	AllDay    bool
	Done      bool
	Start     *time.Time
	End       *time.Time
	Rank      float64
}

func (h Hit) Href() string {
	if h.Kind == TypeTodo {
		return "/todos/" + h.ID.String()
	}
	return "/events/" + h.ID.String()
}

func (h Hit) Hex() string {
	return view.Hex(h.Color)
}

type Result struct {
	Query    Query
	Hits     []Hit
	More     bool
	Offset   int
	Limit    int
	Warnings []string
}

func (r Result) Next() int {
	return r.Offset + r.Limit
}

func (r Result) Prev() int {
	return max(0, r.Offset-r.Limit)
}

type Repo interface {
	UserByID(ctx context.Context, id domain.ID) (domain.User, error)
	Search(ctx context.Context, p Params) ([]Hit, error)
}

type Service struct {
	repo  Repo
	clock clock.Clock
}

func NewService(repo Repo, clk clock.Clock) *Service {
	if clk == nil {
		clk = clock.New()
	}
	return &Service{repo: repo, clock: clk}
}

type Request struct {
	Raw    string
	Type   string
	Limit  int
	Offset int
}

func (s *Service) Search(ctx context.Context, owner domain.ID, req Request) (Result, error) {
	u, err := s.repo.UserByID(ctx, owner)
	if err != nil {
		return Result{}, err
	}
	loc := u.Location()
	now := s.clock.Now()
	today := view.Date(now.In(loc))
	q := Parse(req.Raw, today)
	if req.Type != "" && q.Type == TypeAny {
		if kind, ok := types[strings.ToLower(req.Type)]; ok {
			q.Type = kind
		}
	}
	limit := req.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	limit = min(limit, MaxLimit)
	offset := max(req.Offset, 0)
	res := Result{Query: q, Limit: limit, Offset: offset}
	if q.Empty() {
		res.Warnings = q.Warnings
		return res, nil
	}
	q = narrow(q)
	res.Query, res.Warnings = q, q.Warnings
	if q.Type == typeNone {
		return res, nil
	}
	hits, err := s.repo.Search(ctx, Params{
		Owner: owner, Query: q, Now: now.UTC(), Today: today, Limit: limit + 1, Offset: offset,
	})
	if err != nil {
		return res, err
	}
	if len(hits) > limit {
		hits, res.More = hits[:limit], true
	}
	for i := range hits {
		hits[i].Snippet = markdown.Plain(hits[i].Snippet, snippetRunes)
	}
	res.Hits = hits
	return res, nil
}

// narrow drops kinds no filter can match and says so, instead of returning an
// empty page with no explanation.
func narrow(q Query) Query {
	p := &q
	events, todos := q.Wants(TypeEvent), q.Wants(TypeTodo)
	blockedEvents, blockedTodos := q.Blocks(TypeEvent), q.Blocks(TypeTodo)
	switch {
	case events && todos && blockedEvents != "" && blockedTodos != "":
		p.Type = typeNone
		p.warn("%s and %s cannot both hold for one entry.", blockedEvents, blockedTodos)
	case events && blockedEvents != "":
		p.Type = TypeTodo
		if !todos {
			p.Type = typeNone
		}
		p.warn("%s applies to todos, so events were left out.", blockedEvents)
	case todos && blockedTodos != "":
		p.Type = TypeEvent
		if !events {
			p.Type = typeNone
		}
		p.warn("%s applies to events, so todos were left out.", blockedTodos)
	}
	return q
}

func (h Hit) When(loc *time.Location, clock24 bool) string {
	if h.Start == nil {
		return ""
	}
	t := h.Start.UTC()
	if h.AllDay {
		return t.Format("Mon 2 Jan 2006")
	}
	lt := h.Start.In(loc)
	return lt.Format("Mon 2 Jan 2006") + " " + view.Clock(lt, clock24)
}
