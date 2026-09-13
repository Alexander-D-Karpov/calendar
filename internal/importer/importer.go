package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/csvfmt"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/ical"
	"github.com/Alexander-D-Karpov/calendar/internal/store"
	"github.com/Alexander-D-Karpov/calendar/internal/transfer"
	"github.com/Alexander-D-Karpov/calendar/internal/yamlfmt"
)

const (
	TargetNew  = "new"
	previewMax = 50
	payloadTTL = 24 * time.Hour
)

type Repo interface {
	ListCalendars(ctx context.Context, owner domain.ID) ([]domain.Calendar, error)
	ListTodoLists(ctx context.Context, owner domain.ID) ([]domain.TodoList, error)
	CountCalendars(ctx context.Context, owner domain.ID) (int, error)
	CountTodoLists(ctx context.Context, owner domain.ID) (int, error)
	UserByID(ctx context.Context, id domain.ID) (domain.User, error)
	CreateImport(ctx context.Context, im domain.Import) (domain.Import, error)
	Import(ctx context.Context, owner, id domain.ID) (domain.Import, error)
	ImportByID(ctx context.Context, id domain.ID) (domain.Import, error)
	ListImports(ctx context.Context, owner domain.ID, limit int) ([]domain.Import, error)
	SaveImport(ctx context.Context, im domain.Import) (domain.Import, error)
	ClaimImport(ctx context.Context, owner, id domain.ID) error
	WithImport(ctx context.Context, owner domain.ID, fn func(store.ImportTx) error) error
}

type Options struct {
	Repo     Repo
	Clock    clock.Clock
	MaxItems int
	OnCommit func(ctx context.Context, owner domain.ID) error
}

type Service struct {
	repo     Repo
	clock    clock.Clock
	max      int
	onCommit func(ctx context.Context, owner domain.ID) error
}

func New(o Options) *Service {
	if o.Clock == nil {
		o.Clock = clock.New()
	}
	if o.MaxItems <= 0 {
		o.MaxItems = 50000
	}
	return &Service{repo: o.Repo, clock: o.Clock, max: o.MaxItems, onCommit: o.OnCommit}
}

type Target struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Target   string `json:"target"`
	Events   int    `json:"events"`
	Todos    int    `json:"todos"`
	Skip     bool   `json:"skip"`
	Index    int    `json:"index"`
	Timezone string `json:"timezone,omitempty"`
}

type Stats struct {
	New       int `json:"new"`
	Updated   int `json:"updated"`
	Duplicate int `json:"duplicate"`
	Invalid   int `json:"invalid"`
	Skipped   int `json:"skipped"`
}

type Sample struct {
	Where  string `json:"where"`
	Title  string `json:"title"`
	When   string `json:"when"`
	Action string `json:"action"`
}

type Preview struct {
	Source   string             `json:"source"`
	Filename string             `json:"filename"`
	Timezone string             `json:"timezone"`
	Targets  []Target           `json:"targets"`
	Stats    Stats              `json:"stats"`
	Samples  []Sample           `json:"samples"`
	Problems []transfer.Problem `json:"problems"`
	Settings bool               `json:"settings"`
}

type Choice struct {
	Index  int
	Target string
	Skip   bool
}

type Commit struct {
	Targets []Choice
}

func (s *Service) now() time.Time {
	return s.clock.Now().UTC()
}

func Detect(filename string, data []byte) string {
	head := strings.ToLower(strings.TrimLeft(string(data[:min(len(data), 512)]), "\ufeff \t\r\n"))
	switch {
	case strings.HasPrefix(head, "begin:vcalendar"):
		return domain.SourceICS
	case strings.HasPrefix(head, "format: calendar"), strings.HasPrefix(head, "format: \"calendar\""):
		return domain.SourceYAML
	}
	switch strings.ToLower(filename[max(0, strings.LastIndexByte(filename, '.')):]) {
	case ".ics", ".ical", ".ifb":
		return domain.SourceICS
	case ".yaml", ".yml":
		return domain.SourceYAML
	case ".csv", ".tsv", ".txt":
		return domain.SourceCSV
	}
	return domain.SourceCSV
}

func (s *Service) parse(source string, data []byte, tz string) (transfer.Set, error) {
	switch source {
	case domain.SourceICS, domain.SourceURL:
		return ical.Decode(data, ical.Options{Timezone: tz, MaxItems: s.max})
	case domain.SourceYAML:
		return yamlfmt.Decode(data, yamlfmt.Options{Timezone: tz, MaxItems: s.max})
	case domain.SourceCSV:
		return csvfmt.Decode(data, csvfmt.Options{Timezone: tz, MaxItems: s.max})
	}
	return transfer.Set{}, fmt.Errorf("%w: unknown file type", domain.ErrInvalid)
}

func (s *Service) List(ctx context.Context, owner domain.ID, limit int) ([]domain.Import, error) {
	return s.repo.ListImports(ctx, owner, limit)
}

func (s *Service) Get(ctx context.Context, owner, id domain.ID) (domain.Import, Preview, error) {
	im, err := s.repo.Import(ctx, owner, id)
	if err != nil {
		return im, Preview{}, err
	}
	var p Preview
	if len(im.Preview) > 0 {
		_ = json.Unmarshal(im.Preview, &p)
	}
	return im, p, nil
}

func (s *Service) Upload(ctx context.Context, owner domain.ID, filename string, data []byte) (domain.Import, Preview, error) {
	if len(data) == 0 {
		return domain.Import{}, Preview{}, fmt.Errorf("%w: the file is empty", domain.ErrInvalid)
	}
	u, err := s.repo.UserByID(ctx, owner)
	if err != nil {
		return domain.Import{}, Preview{}, err
	}
	source := Detect(filename, data)
	set, err := s.parse(source, data, u.Timezone)
	if err != nil {
		return domain.Import{}, Preview{}, err
	}
	p, err := s.preview(ctx, owner, set, source, filename, u.Timezone)
	if err != nil {
		return domain.Import{}, Preview{}, err
	}
	body, err := json.Marshal(p)
	if err != nil {
		return domain.Import{}, Preview{}, err
	}
	sum := sha256.Sum256(data)
	im, err := s.repo.CreateImport(ctx, domain.Import{
		ID: domain.NewID(), OwnerID: owner, Source: source, Filename: transfer.Clip(filename, 255),
		Status: domain.ImportPreviewed, Payload: data, PayloadSum: sum[:], Preview: body,
		ExpiresAt: s.now().Add(payloadTTL),
	})
	return im, p, err
}

func (s *Service) preview(ctx context.Context, owner domain.ID, set transfer.Set, source, filename, tz string) (Preview, error) {
	p := Preview{Source: source, Filename: filename, Timezone: tz, Problems: set.Problems, Settings: set.Settings != nil}
	p.Stats.Invalid = len(set.Problems)
	cals, err := s.repo.ListCalendars(ctx, owner)
	if err != nil {
		return p, err
	}
	lists, err := s.repo.ListTodoLists(ctx, owner)
	if err != nil {
		return p, err
	}
	err = s.repo.WithImport(ctx, owner, func(tx store.ImportTx) error {
		for i, c := range set.Calendars {
			t := Target{Name: c.Name, Kind: domain.BindCalendar, Index: i, Timezone: c.Timezone, Target: TargetNew}
			for _, sr := range c.Series {
				t.Events += 1 + len(sr.Overrides)
			}
			if t.Events == 0 {
				continue
			}
			if m := match(c.Name, calNames(cals)); m != "" {
				t.Target = m
			}
			p.Targets = append(p.Targets, t)
		}
		for i, l := range set.Lists {
			t := Target{Name: l.Name, Kind: domain.BindTaskList, Index: i, Target: TargetNew}
			for _, n := range l.Todos {
				t.Todos += 1 + len(n.Subtasks)
			}
			if t.Todos == 0 {
				continue
			}
			if m := match(l.Name, listNames(lists)); m != "" {
				t.Target = m
			}
			p.Targets = append(p.Targets, t)
		}
		return s.classify(ctx, tx, set, &p)
	})
	return p, err
}

func calNames(cals []domain.Calendar) map[string]string {
	out := make(map[string]string, len(cals))
	for _, c := range cals {
		out[strings.ToLower(c.Name)] = c.ID.String()
	}
	return out
}

func listNames(lists []domain.TodoList) map[string]string {
	out := make(map[string]string, len(lists))
	for _, l := range lists {
		out[strings.ToLower(l.Name)] = l.ID.String()
	}
	return out
}

func match(name string, known map[string]string) string {
	return known[strings.ToLower(strings.TrimSpace(name))]
}

func (s *Service) classify(ctx context.Context, tx store.ImportTx, set transfer.Set, p *Preview) error {
	for _, t := range p.Targets {
		id, err := domain.ParseID(t.Target)
		if err != nil {
			if t.Kind == domain.BindCalendar {
				p.Stats.New += t.Events
				s.samples(p, set.Calendars[t.Index], "new")
			} else {
				p.Stats.New += t.Todos
			}
			continue
		}
		if t.Kind == domain.BindCalendar {
			if err := s.classifyEvents(ctx, tx, set.Calendars[t.Index], id, p); err != nil {
				return err
			}
			continue
		}
		if err := s.classifyTodos(ctx, tx, set.Lists[t.Index], id, p); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) classifyEvents(ctx context.Context, tx store.ImportTx, c transfer.Calendar, cal domain.ID, p *Preview) error {
	fps := make([][]byte, 0, len(c.Series))
	for _, sr := range c.Series {
		fps = append(fps, domain.EventFingerprint(sr.Master))
	}
	seen, err := tx.EventFingerprints(ctx, cal, fps)
	if err != nil {
		return err
	}
	for i, sr := range c.Series {
		action := "new"
		if sr.Master.UID != "" {
			switch _, err := tx.EventByUID(ctx, cal, sr.Master.UID); {
			case err == nil:
				action = "updated"
			case !errors.Is(err, domain.ErrNotFound):
				return err
			}
		}
		if action == "new" && seen[hex.EncodeToString(fps[i])] {
			action = "duplicate"
		}
		n := 1 + len(sr.Overrides)
		switch action {
		case "updated":
			p.Stats.Updated += n
		case "duplicate":
			p.Stats.Duplicate += n
		default:
			p.Stats.New += n
		}
		addSample(p, sample(sr.Where, sr.Master, action))
	}
	return nil
}

func (s *Service) classifyTodos(ctx context.Context, tx store.ImportTx, l transfer.List, list domain.ID, p *Preview) error {
	fps := make([][]byte, 0, len(l.Todos))
	for _, n := range l.Todos {
		fps = append(fps, domain.TodoFingerprint(n.Todo))
	}
	seen, err := tx.TodoFingerprints(ctx, list, fps)
	if err != nil {
		return err
	}
	for i, n := range l.Todos {
		action := "new"
		if n.Todo.UID != "" {
			switch _, err := tx.TodoByUID(ctx, list, n.Todo.UID); {
			case err == nil:
				action = "updated"
			case !errors.Is(err, domain.ErrNotFound):
				return err
			}
		}
		if action == "new" && seen[hex.EncodeToString(fps[i])] {
			action = "duplicate"
		}
		count := 1 + len(n.Subtasks)
		switch action {
		case "updated":
			p.Stats.Updated += count
		case "duplicate":
			p.Stats.Duplicate += count
		default:
			p.Stats.New += count
		}
		when := ""
		if n.Todo.DueDate != nil {
			when = n.Todo.DueDate.Format(domain.DateLayout)
		}
		addSample(p, Sample{Where: n.Where, Title: n.Todo.Title, When: when, Action: action})
	}
	return nil
}

func (s *Service) samples(p *Preview, c transfer.Calendar, action string) {
	for _, sr := range c.Series {
		addSample(p, sample(sr.Where, sr.Master, action))
	}
}

func sample(where string, e domain.Event, action string) Sample {
	when := e.Start.UTC().Format(domain.DateLayout)
	if !e.AllDay {
		when = e.Start.In(e.Zone()).Format("2006-01-02 15:04")
	}
	title := e.Title
	if title == "" {
		title = "(no title)"
	}
	return Sample{Where: where, Title: transfer.Clip(title, 80), When: when, Action: action}
}

func addSample(p *Preview, s Sample) {
	if len(p.Samples) < previewMax {
		p.Samples = append(p.Samples, s)
	}
}

func (s *Service) Commit(ctx context.Context, owner, id domain.ID, c Commit) (Stats, error) {
	im, err := s.repo.Import(ctx, owner, id)
	if err != nil {
		return Stats{}, err
	}
	if im.Status != domain.ImportPreviewed {
		return Stats{}, fmt.Errorf("%w: this import was already handled", domain.ErrConflict)
	}
	if len(im.Payload) == 0 {
		return Stats{}, fmt.Errorf("%w: the uploaded file expired, upload it again", domain.ErrConflict)
	}
	var p Preview
	if err := json.Unmarshal(im.Preview, &p); err != nil {
		return Stats{}, err
	}
	choices := make(map[int]Choice, len(c.Targets))
	for _, ch := range c.Targets {
		choices[ch.Index] = ch
	}
	targets := make([]Target, 0, len(p.Targets))
	for _, t := range p.Targets {
		if ch, ok := choices[t.Index]; ok {
			t.Skip = ch.Skip
			if ch.Target != "" {
				t.Target = ch.Target
			}
		}
		targets = append(targets, t)
	}
	if err := s.repo.ClaimImport(ctx, owner, id); err != nil {
		return Stats{}, err
	}
	stats, err := s.apply(ctx, owner, im, p, targets)
	im.Status, im.FinishedAt = domain.ImportDone, ptr(s.now())
	im.Payload = nil
	if err != nil {
		im.Status, im.Error = domain.ImportFailed, transfer.Clip(err.Error(), 500)
	}
	if body, merr := json.Marshal(stats); merr == nil {
		im.Stats = body
	}
	if _, serr := s.repo.SaveImport(ctx, im); serr != nil {
		return stats, errors.Join(err, serr)
	}
	if err == nil && s.onCommit != nil {
		// A failed duplicate scan must not fail the import; the nightly sweep retries.
		_ = s.onCommit(ctx, owner)
	}
	return stats, err
}

func ptr[T any](v T) *T {
	return &v
}

func (s *Service) apply(ctx context.Context, owner domain.ID, im domain.Import, p Preview, targets []Target) (Stats, error) {
	u, err := s.repo.UserByID(ctx, owner)
	if err != nil {
		return Stats{}, err
	}
	set, err := s.parse(im.Source, im.Payload, p.Timezone)
	if err != nil {
		return Stats{}, err
	}
	var stats Stats
	stats.Invalid = len(set.Problems)
	ctx = domain.WithOrigin(ctx, domain.OriginImport)
	err = s.repo.WithImport(ctx, owner, func(tx store.ImportTx) error {
		w := &writer{tx: tx, owner: owner, now: s.now(), userTZ: u.Timezone, stats: &stats}
		for _, t := range targets {
			if t.Skip {
				stats.Skipped += t.Events + t.Todos
				continue
			}
			if t.Kind == domain.BindCalendar {
				if err := w.calendar(ctx, set.Calendars[t.Index], t); err != nil {
					return err
				}
				continue
			}
			if err := w.list(ctx, set.Lists[t.Index], t); err != nil {
				return err
			}
		}
		return nil
	})
	return stats, err
}
