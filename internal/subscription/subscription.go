package subscription

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/ical"
	"github.com/Alexander-D-Karpov/calendar/internal/jobs"
	"github.com/Alexander-D-Karpov/calendar/internal/observe"
	"github.com/Alexander-D-Karpov/calendar/internal/store"
	"github.com/Alexander-D-Karpov/calendar/internal/transfer"
)

const (
	maxFailures = 5
	maxErrorLen = 500
	dueBatch    = 200
)

type Job struct {
	Subscription domain.ID `json:"subscription"`
}

type Repo interface {
	UserByID(ctx context.Context, id domain.ID) (domain.User, error)
	CountCalendars(ctx context.Context, owner domain.ID) (int, error)
	Calendar(ctx context.Context, owner, id domain.ID) (domain.Calendar, error)
	DeleteCalendar(ctx context.Context, owner, id domain.ID, fn func(domain.Calendar) error) error
	CreateSubscription(ctx context.Context, sub domain.Subscription, cal domain.Calendar) (domain.Subscription, error)
	Subscription(ctx context.Context, owner, id domain.ID) (domain.Subscription, error)
	SubscriptionByID(ctx context.Context, id domain.ID) (domain.Subscription, error)
	Subscriptions(ctx context.Context, owner domain.ID) ([]domain.Subscription, error)
	SaveSubscriptionState(ctx context.Context, sub domain.Subscription) error
	DueSubscriptions(ctx context.Context, now time.Time, limit int) ([]domain.ID, error)
	CalendarEvents(ctx context.Context, owner, calendar domain.ID) ([]domain.Event, error)
	WithImport(ctx context.Context, owner domain.ID, fn func(store.ImportTx) error) error
}

type Options struct {
	Repo            Repo
	Keys            *crypto.KeyRing
	Client          *http.Client
	Clock           clock.Clock
	Logger          *slog.Logger
	Enqueue         func(ctx context.Context, kind string, payload any, o jobs.Options) error
	DefaultInterval time.Duration
	MinInterval     time.Duration
	MaxSize         int64
	MaxItems        int
}

type Service struct {
	repo     Repo
	keys     *crypto.KeyRing
	client   *http.Client
	clock    clock.Clock
	logger   *slog.Logger
	enqueue  func(ctx context.Context, kind string, payload any, o jobs.Options) error
	interval time.Duration
	minEvery time.Duration
	maxSize  int64
	maxItems int
}

type Status struct {
	domain.Subscription
	URL      string
	Calendar domain.Calendar
}

func New(o Options) *Service {
	if o.Clock == nil {
		o.Clock = clock.New()
	}
	if o.DefaultInterval <= 0 {
		o.DefaultInterval = time.Hour
	}
	if o.MinInterval <= 0 {
		o.MinInterval = 15 * time.Minute
	}
	return &Service{
		repo: o.Repo, keys: o.Keys, client: o.Client, clock: o.Clock, logger: o.Logger, enqueue: o.Enqueue,
		interval: o.DefaultInterval, minEvery: o.MinInterval, maxSize: o.MaxSize, maxItems: o.MaxItems,
	}
}

func urlAAD(id domain.ID) []byte {
	return crypto.AAD("subscription_url", id.String())
}

func (s *Service) now() time.Time {
	return s.clock.Now().UTC()
}

func Normalize(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if rest, ok := strings.CutPrefix(strings.ToLower(raw), "webcal://"); ok {
		raw = "https://" + raw[len(raw)-len(rest):]
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		var v domain.ValidationError
		v.Add("url", "must be an http, https or webcal link to an .ics file")
		return nil, v.Err()
	}
	u.Fragment = ""
	return u, nil
}

func (s *Service) List(ctx context.Context, owner domain.ID) ([]Status, error) {
	subs, err := s.repo.Subscriptions(ctx, owner)
	if err != nil {
		return nil, err
	}
	out := make([]Status, 0, len(subs))
	for _, sub := range subs {
		st := Status{Subscription: sub}
		if u, err := s.keys.OpenString(sub.URLEnc, urlAAD(sub.ID)); err == nil {
			st.URL = u
		}
		if cal, err := s.repo.Calendar(ctx, owner, sub.CalendarID); err == nil {
			st.Calendar = cal
		}
		out = append(out, st)
	}
	return out, nil
}

func (s *Service) Create(ctx context.Context, owner domain.ID, raw, name string, every time.Duration) (domain.Subscription, error) {
	u, err := Normalize(raw)
	if err != nil {
		return domain.Subscription{}, err
	}
	n, err := s.repo.CountCalendars(ctx, owner)
	if err != nil {
		return domain.Subscription{}, err
	}
	if n >= domain.MaxCalendars {
		return domain.Subscription{}, fmt.Errorf("%w: an account can have at most %d calendars", domain.ErrConflict, domain.MaxCalendars)
	}
	if every <= 0 {
		every = s.interval
	}
	every = max(every, s.minEvery)
	name = strings.TrimSpace(name)
	if name == "" {
		name = u.Host
	}
	sub := domain.Subscription{
		ID: domain.NewID(), OwnerID: owner, CalendarID: domain.NewID(), Host: transfer.Clip(u.Host, 200),
		Interval: every, NextRefreshAt: s.now(), Status: domain.SubOK,
	}
	sub.URLEnc = s.keys.SealString(u.String(), urlAAD(sub.ID))
	cal := domain.Calendar{
		ID: sub.CalendarID, OwnerID: owner, Name: transfer.Clip(name, 100), Color: domain.DefaultCalendarColor,
		Kind: domain.CalendarSubscription, ReadOnly: true, Position: -1, DefaultReminders: []int{},
	}
	if sub, err = s.repo.CreateSubscription(ctx, sub, cal); err != nil {
		return sub, err
	}
	return sub, s.queue(ctx, sub.ID, time.Time{})
}

func (s *Service) Refresh(ctx context.Context, owner, id domain.ID) error {
	sub, err := s.repo.Subscription(ctx, owner, id)
	if err != nil {
		return err
	}
	sub.Status, sub.Failures, sub.NextRefreshAt = domain.SubOK, 0, s.now()
	if err := s.repo.SaveSubscriptionState(ctx, sub); err != nil {
		return err
	}
	return s.queue(ctx, sub.ID, time.Time{})
}

func (s *Service) Delete(ctx context.Context, owner, id domain.ID) error {
	sub, err := s.repo.Subscription(ctx, owner, id)
	if err != nil {
		return err
	}
	err = s.repo.DeleteCalendar(ctx, owner, sub.CalendarID, func(domain.Calendar) error { return nil })
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	return err
}

func (s *Service) queue(ctx context.Context, id domain.ID, at time.Time) error {
	return s.enqueue(ctx, jobs.KindSubscription, Job{Subscription: id}, jobs.Options{UniqueKey: id.String(), RunAt: at})
}

func (s *Service) HandleDue(ctx context.Context, _ jobs.Job) error {
	ids, err := s.repo.DueSubscriptions(ctx, s.now(), dueBatch)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.queue(ctx, id, time.Time{}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) HandleRefresh(ctx context.Context, j jobs.Job) error {
	var p Job
	if err := j.Decode(&p); err != nil {
		return jobs.Permanent(err)
	}
	sub, err := s.repo.SubscriptionByID(ctx, p.Subscription)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if sub.Status == domain.SubPaused {
		return nil
	}
	now := s.now()
	sub.LastAttemptAt = &now
	err = s.refresh(ctx, &sub)
	switch {
	case err == nil:
		sub.Status, sub.Failures, sub.LastError, sub.LastOKAt = domain.SubOK, 0, "", &now
		sub.NextRefreshAt = now.Add(sub.Interval)
	default:
		sub.Failures++
		sub.LastError = transfer.Clip(observe.ScrubString(err.Error()), maxErrorLen)
		sub.NextRefreshAt = now.Add(backoff(sub.Interval, sub.Failures))
		if sub.Failures >= maxFailures {
			sub.Status = domain.SubError
		}
		s.logger.LogAttrs(ctx, slog.LevelWarn, "subscription refresh failed",
			slog.String("subscription", sub.ID.String()), slog.String("host", sub.Host),
			slog.Int("failures", sub.Failures), slog.Any("err", err))
	}
	return s.repo.SaveSubscriptionState(ctx, sub)
}

func backoff(every time.Duration, failures int) time.Duration {
	return min(every<<min(max(failures-1, 0), 5), 24*time.Hour)
}

func (s *Service) refresh(ctx context.Context, sub *domain.Subscription) error {
	raw, err := s.keys.OpenString(sub.URLEnc, urlAAD(sub.ID))
	if err != nil {
		return jobs.Permanent(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return jobs.Permanent(err)
	}
	req.Header.Set("Accept", "text/calendar, */*;q=0.5")
	req.Header.Set("User-Agent", "calendar/"+ical.ProductID())
	if sub.ETag != "" {
		req.Header.Set("If-None-Match", sub.ETag)
	}
	if sub.LastModified != "" {
		req.Header.Set("If-Modified-Since", sub.LastModified)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()
	if resp.StatusCode == http.StatusNotModified {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the feed answered %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, s.maxSize+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > s.maxSize {
		return fmt.Errorf("the feed is larger than %d bytes", s.maxSize)
	}
	sum := sha256.Sum256(body)
	if slices.Equal(sum[:], sub.ContentHash) {
		sub.ETag, sub.LastModified = resp.Header.Get("ETag"), resp.Header.Get("Last-Modified")
		return nil
	}
	u, err := s.repo.UserByID(ctx, sub.OwnerID)
	if err != nil {
		return err
	}
	set, err := ical.Decode(body, ical.Options{Timezone: u.Timezone, MaxItems: s.maxItems})
	if err != nil {
		return err
	}
	if err := s.merge(ctx, *sub, set, sum[:]); err != nil {
		return err
	}
	sub.ETag, sub.LastModified, sub.ContentHash = resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"), sum[:]
	return nil
}
