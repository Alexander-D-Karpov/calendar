package dedup

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/jobs"
	"github.com/Alexander-D-Karpov/calendar/internal/store"
)

const (
	fuzzyWindow    = 15 * time.Minute
	fuzzyThreshold = 0.8
	scanBack       = 180 * 24 * time.Hour
	scanAhead      = 400 * 24 * time.Hour
	maxTodos       = 5000
	maxPairs       = 500
	reviewLimit    = 200
	// Wide enough to cover any calendar anyone keeps, without reaching the
	// timestamp bounds Postgres would reject.
	fullScanSpan = 100 * 365 * 24 * time.Hour
)

type ScanJob struct {
	Owner domain.ID `json:"owner"`
	// Full ignores the rolling window below. The periodic sweep stays windowed
	// so it costs the same on a large account every night, but that silently
	// hides anything older or further out, which is where a bad import leaves
	// most of its duplicates.
	Full bool `json:"full,omitempty"`
}

type Repo interface {
	UserByID(ctx context.Context, id domain.ID) (domain.User, error)
	Owners(ctx context.Context) ([]domain.ID, error)
	ListCalendars(ctx context.Context, owner domain.ID) ([]domain.Calendar, error)
	ListTodoLists(ctx context.Context, owner domain.ID) ([]domain.TodoList, error)
	EventsInSpan(ctx context.Context, owner domain.ID, from, to time.Time) ([]domain.Event, error)
	OpenTodos(ctx context.Context, owner domain.ID, limit int) ([]domain.Todo, error)
	Similarity(ctx context.Context, a, b string) (float64, error)
	Duplicates(ctx context.Context, owner domain.ID, status string, limit int) ([]domain.Duplicate, error)
	CountDuplicates(ctx context.Context, owner domain.ID) (int, error)
	Resolved(ctx context.Context, owner domain.ID, entity string, a, b domain.ID) (bool, error)
	WithDedup(ctx context.Context, owner domain.ID, fn func(store.DedupTx) error) error
}

type Options struct {
	Repo    Repo
	Clock   clock.Clock
	Logger  *slog.Logger
	Enqueue func(ctx context.Context, kind string, payload any, o jobs.Options) error
}

type Service struct {
	repo    Repo
	clock   clock.Clock
	logger  *slog.Logger
	enqueue func(ctx context.Context, kind string, payload any, o jobs.Options) error
}

func New(o Options) *Service {
	if o.Clock == nil {
		o.Clock = clock.New()
	}
	return &Service{repo: o.Repo, clock: o.Clock, logger: o.Logger, enqueue: o.Enqueue}
}

func (s *Service) now() time.Time {
	return s.clock.Now().UTC()
}

func (s *Service) Scan(ctx context.Context, owner domain.ID) error {
	return s.enqueue(ctx, jobs.KindDedupScan, ScanJob{Owner: owner}, jobs.Options{UniqueKey: owner.String()})
}

func (s *Service) HandleSweep(ctx context.Context, _ jobs.Job) error {
	owners, err := s.repo.Owners(ctx)
	if err != nil {
		return err
	}
	for _, id := range owners {
		u, err := s.repo.UserByID(ctx, id)
		if err != nil {
			return err
		}
		if u.DedupPolicy == domain.DedupOff {
			continue
		}
		if err := s.Scan(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) HandleScan(ctx context.Context, j jobs.Job) error {
	var p ScanJob
	if err := j.Decode(&p); err != nil {
		return jobs.Permanent(err)
	}
	u, err := s.repo.UserByID(ctx, p.Owner)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if u.DedupPolicy == domain.DedupOff {
		return nil
	}
	pairs, err := s.find(ctx, p.Owner, p.Full)
	if err != nil {
		return err
	}
	if len(pairs) == 0 {
		return nil
	}
	flagged, removed := 0, 0
	ctx = domain.WithOrigin(ctx, domain.OriginDedup)
	err = s.repo.WithDedup(ctx, p.Owner, func(tx store.DedupTx) error {
		for _, d := range pairs {
			if !d.Auto(u.DedupPolicy) {
				d.ID = domain.NewID()
				if err := tx.SaveDuplicate(ctx, d); err != nil {
					return err
				}
				flagged++
				continue
			}
			switch err := s.remove(ctx, tx, p.Owner, d, d.BID); {
			case err == nil:
				removed++
			case errors.Is(err, domain.ErrNotFound):
			default:
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.logger.LogAttrs(ctx, slog.LevelInfo, "duplicate scan finished",
		slog.String("owner", p.Owner.String()), slog.Int("flagged", flagged), slog.Int("deleted", removed))
	return nil
}

func (s *Service) find(ctx context.Context, owner domain.ID, full bool) ([]domain.Duplicate, error) {
	now := s.now()
	back, ahead := scanBack, scanAhead
	if full {
		back, ahead = fullScanSpan, fullScanSpan
	}
	events, err := s.repo.EventsInSpan(ctx, owner, now.Add(-back), now.Add(ahead))
	if err != nil {
		return nil, err
	}
	todos, err := s.repo.OpenTodos(ctx, owner, maxTodos)
	if err != nil {
		return nil, err
	}
	var out []domain.Duplicate
	add := func(d domain.Duplicate) bool {
		resolved, err := s.repo.Resolved(ctx, owner, d.Entity, d.AID, d.BID)
		if err != nil || resolved {
			return err == nil
		}
		out = append(out, d)
		return len(out) < maxPairs
	}
	if !s.events(ctx, owner, events, add) {
		return out, nil
	}
	s.todos(ctx, owner, todos, add)
	return out, nil
}

func (s *Service) events(ctx context.Context, owner domain.ID, events []domain.Event, add func(domain.Duplicate) bool) bool {
	byUID := map[string][]domain.Event{}
	byPrint := map[string][]domain.Event{}
	for _, e := range events {
		if e.UID != "" {
			byUID[e.UID] = append(byUID[e.UID], e)
		}
		byPrint[hex.EncodeToString(domain.EventFingerprint(e))] = append(byPrint[hex.EncodeToString(domain.EventFingerprint(e))], e)
	}
	seen := map[string]bool{}
	pair := func(a, b domain.Event, reason string, score float64) bool {
		k := pairKey(a.ID, b.ID)
		if seen[k] {
			return true
		}
		seen[k] = true
		return add(duplicate(owner, domain.EntityEvent, a.ID, b.ID, reason, score, eventSnapshot(a), eventSnapshot(b)))
	}
	for _, group := range byUID {
		for i := 1; i < len(group); i++ {
			if !pair(group[0], group[i], domain.ReasonUID, 1) {
				return false
			}
		}
	}
	for _, group := range byPrint {
		for i := 1; i < len(group); i++ {
			if !pair(group[0], group[i], domain.ReasonFingerprint, 1) {
				return false
			}
		}
	}
	timed := slices.Clone(events)
	slices.SortFunc(timed, func(a, b domain.Event) int { return a.Start.Compare(b.Start) })
	for i, a := range timed {
		if strings.TrimSpace(a.Title) == "" {
			continue
		}
		for j := i + 1; j < len(timed); j++ {
			b := timed[j]
			if b.Start.Sub(a.Start) > fuzzyWindow {
				break
			}
			if a.AllDay != b.AllDay || strings.TrimSpace(b.Title) == "" || seen[pairKey(a.ID, b.ID)] {
				continue
			}
			score, err := s.repo.Similarity(ctx, a.Title, b.Title)
			if err != nil || score < fuzzyThreshold {
				continue
			}
			if !pair(a, b, domain.ReasonFuzzy, score) {
				return false
			}
		}
	}
	return true
}

func (s *Service) todos(ctx context.Context, owner domain.ID, todos []domain.Todo, add func(domain.Duplicate) bool) {
	byUID := map[string][]domain.Todo{}
	byPrint := map[string][]domain.Todo{}
	for _, t := range todos {
		if t.UID != "" {
			byUID[t.UID] = append(byUID[t.UID], t)
		}
		k := hex.EncodeToString(domain.TodoFingerprint(t)) + "\x00" + t.ListID.String()
		byPrint[k] = append(byPrint[k], t)
	}
	seen := map[string]bool{}
	pair := func(a, b domain.Todo, reason string) bool {
		k := pairKey(a.ID, b.ID)
		if seen[k] {
			return true
		}
		seen[k] = true
		return add(duplicate(owner, domain.EntityTodo, a.ID, b.ID, reason, 1, todoSnapshot(a), todoSnapshot(b)))
	}
	for _, group := range byUID {
		for i := 1; i < len(group); i++ {
			if !pair(group[0], group[i], domain.ReasonUID) {
				return
			}
		}
	}
	for _, group := range byPrint {
		for i := 1; i < len(group); i++ {
			if !pair(group[0], group[i], domain.ReasonFingerprint) {
				return
			}
		}
	}
}

func pairKey(a, b domain.ID) string {
	x, y := a.String(), b.String()
	if y < x {
		x, y = y, x
	}
	return x + "\x00" + y
}

func duplicate(owner domain.ID, entity string, a, b domain.ID, reason string, score float64, sa, sb []byte) domain.Duplicate {
	d := domain.Duplicate{OwnerID: owner, Entity: entity, AID: a, BID: b, Reason: reason, Score: score, A: sa, B: sb}
	if b.String() < a.String() {
		d.AID, d.BID, d.A, d.B = b, a, sb, sa
	}
	return d
}

type Snapshot struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	When     string   `json:"when"`
	Where    string   `json:"where"`
	Body     string   `json:"body,omitempty"`
	Location string   `json:"location,omitempty"`
	Extra    []string `json:"extra,omitempty"`
	Created  string   `json:"created"`
}

func eventSnapshot(e domain.Event) []byte {
	s := Snapshot{ID: e.ID.String(), Title: e.Title, Body: e.Body, Location: e.Location, Created: e.CreatedAt.UTC().Format(time.RFC3339)}
	if e.AllDay {
		s.When = e.Start.UTC().Format(domain.DateLayout)
	} else {
		s.When = e.Start.In(e.Zone()).Format("2006-01-02 15:04")
	}
	if e.RRule != "" {
		s.Extra = append(s.Extra, "repeats")
	}
	if len(e.Reminders) > 0 {
		s.Extra = append(s.Extra, fmt.Sprintf("%d reminders", len(e.Reminders)))
	}
	b, _ := json.Marshal(s)
	return b
}

func todoSnapshot(t domain.Todo) []byte {
	s := Snapshot{ID: t.ID.String(), Title: t.Title, Body: t.Body, Created: t.CreatedAt.UTC().Format(time.RFC3339)}
	if t.DueDate != nil {
		s.When = t.DueDate.Format(domain.DateLayout)
		if t.DueTime != nil {
			s.When += " " + domain.FormatClock(*t.DueTime)
		}
	}
	if n := len(t.Checks); n > 0 {
		s.Extra = append(s.Extra, fmt.Sprintf("%d checklist items", n))
	}
	b, _ := json.Marshal(s)
	return b
}

func Decode(raw []byte) Snapshot {
	var s Snapshot
	_ = json.Unmarshal(raw, &s)
	return s
}
