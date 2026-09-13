package gsync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/google"
	"github.com/Alexander-D-Karpov/calendar/internal/jobs"
	"github.com/Alexander-D-Karpov/calendar/internal/metrics"
	"github.com/Alexander-D-Karpov/calendar/internal/observe"
	"github.com/Alexander-D-Karpov/calendar/internal/store"
)

const (
	WebhookPath  = "/hooks/google/calendar"
	TargetNew    = "new"
	pushBatch    = 200
	maxPushTries = 20
	renewBefore  = 24 * time.Hour
	watchedPoll  = time.Hour
	pollBatch    = 500
	webhookDelay = 2 * time.Second
	maxErrorLen  = 500
)

var (
	ErrNotConnected  = fmt.Errorf("%w: Google is not connected", domain.ErrConflict)
	errMasterPending = errors.New("gsync: the series is not synced yet")
	errParentPending = errors.New("gsync: the parent todo is not synced yet")
	errKeepLocal     = errors.New("gsync: the default calendar or list is kept")
)

type BindingJob struct {
	Binding domain.ID `json:"binding"`
}

type Store interface {
	UserByID(ctx context.Context, id domain.ID) (domain.User, error)
	Calendar(ctx context.Context, owner, id domain.ID) (domain.Calendar, error)
	CountCalendars(ctx context.Context, owner domain.ID) (int, error)
	CreateCalendar(ctx context.Context, c domain.Calendar) (domain.Calendar, error)
	DeleteCalendar(ctx context.Context, owner, id domain.ID, fn func(domain.Calendar) error) error
	TodoList(ctx context.Context, owner, id domain.ID) (domain.TodoList, error)
	CountTodoLists(ctx context.Context, owner domain.ID) (int, error)
	CreateTodoList(ctx context.Context, l domain.TodoList) (domain.TodoList, error)
	DeleteTodoList(ctx context.Context, owner, id domain.ID, fn func(domain.TodoList) error) error

	GoogleAccount(ctx context.Context, user domain.ID) (domain.GoogleAccount, error)
	GoogleAccountByID(ctx context.Context, id domain.ID) (domain.GoogleAccount, error)
	SaveGoogleAccount(ctx context.Context, a domain.GoogleAccount) (domain.GoogleAccount, error)
	SetGoogleAccountStatus(ctx context.Context, id domain.ID, status, lastErr string) error
	DeleteGoogleAccount(ctx context.Context, user domain.ID) error

	Bindings(ctx context.Context, owner domain.ID) ([]domain.SyncBinding, error)
	Binding(ctx context.Context, id domain.ID) (domain.SyncBinding, error)
	BindingByChannel(ctx context.Context, channel string) (domain.SyncBinding, error)
	CreateBinding(ctx context.Context, b domain.SyncBinding) (domain.SyncBinding, error)
	UpdateBinding(ctx context.Context, owner, id domain.ID, direction string, enabled bool) (domain.SyncBinding, error)
	DeleteBinding(ctx context.Context, owner, id domain.ID) (domain.SyncBinding, error)
	SaveBindingState(ctx context.Context, b domain.SyncBinding) error
	SaveWatch(ctx context.Context, id domain.ID, channel, resource string, tokenHash []byte, expires *time.Time) error
	DueBindings(ctx context.Context, now time.Time, limit int) ([]domain.ID, error)
	WatchCandidates(ctx context.Context, before time.Time) ([]domain.SyncBinding, error)
	SeedOutbox(ctx context.Context, b domain.SyncBinding) error
	PendingOutbox(ctx context.Context, binding domain.ID, limit int) ([]int64, error)
	NextOutboxAt(ctx context.Context, binding domain.ID) (*time.Time, error)
	OutboxCounts(ctx context.Context, owner domain.ID) (map[domain.ID]int, error)
	WithSync(ctx context.Context, owner, binding domain.ID, fn func(store.SyncTx) error) error
}

type Options struct {
	Store        Store
	Keys         *crypto.KeyRing
	OAuth        *google.OAuth
	Dial         func(ctx context.Context, refresh string) (google.Remote, error)
	Clock        clock.Clock
	Logger       *slog.Logger
	Metrics      *metrics.Metrics
	Enqueue      func(ctx context.Context, kind string, payload any, o jobs.Options) error
	PollInterval time.Duration
	WebhookURL   string
}

type Engine struct {
	store   Store
	keys    *crypto.KeyRing
	oauth   *google.OAuth
	dial    func(ctx context.Context, refresh string) (google.Remote, error)
	clock   clock.Clock
	logger  *slog.Logger
	metrics *metrics.Metrics
	enqueue func(ctx context.Context, kind string, payload any, o jobs.Options) error
	poll    time.Duration
	webhook string
}

type BindingStatus struct {
	domain.SyncBinding
	Pending  int
	Watching bool
}

type Status struct {
	Account  *domain.GoogleAccount
	Bindings []BindingStatus
}

type Remotes struct {
	Calendars []google.Calendar
	Lists     []google.TaskList
}

type Choice struct {
	Entity    string
	RemoteID  string
	Target    string
	Direction string
}

type source struct {
	entity   string
	id       string
	name     string
	color    string
	tz       string
	readOnly bool
}

func New(o Options) *Engine {
	if o.Dial == nil && o.OAuth != nil {
		o.Dial = o.OAuth.Remote
	}
	if o.Clock == nil {
		o.Clock = clock.New()
	}
	if o.PollInterval < time.Minute {
		o.PollInterval = 5 * time.Minute
	}
	return &Engine{
		store: o.Store, keys: o.Keys, oauth: o.OAuth, dial: o.Dial, clock: o.Clock, logger: o.Logger,
		metrics: o.Metrics, enqueue: o.Enqueue, poll: o.PollInterval, webhook: o.WebhookURL,
	}
}

func refreshAAD(user domain.ID) []byte {
	return crypto.AAD("google_refresh", user.String())
}

func scrub(err error) string {
	return clip(observe.ScrubString(err.Error()), maxErrorLen)
}

func gone(err error) bool {
	return errors.Is(err, google.ErrNotFound) || errors.Is(err, google.ErrGone)
}

func retryDelay(attempt int) time.Duration {
	return min(30*time.Second<<min(max(attempt-1, 0), 7), time.Hour)
}

func (e *Engine) now() time.Time {
	return e.clock.Now().UTC()
}

func (e *Engine) remote(ctx context.Context, acct domain.GoogleAccount) (google.Remote, error) {
	rt, err := e.keys.OpenString(acct.RefreshTokenEnc, refreshAAD(acct.UserID))
	if err != nil {
		return nil, jobs.Permanent(fmt.Errorf("gsync: open refresh token: %w", err))
	}
	return e.dial(ctx, rt)
}

func (e *Engine) queue(ctx context.Context, kind string, id domain.ID, at time.Time) error {
	return e.enqueue(ctx, kind, BindingJob{Binding: id}, jobs.Options{UniqueKey: id.String(), RunAt: at})
}

func (e *Engine) failed(ctx context.Context, acct domain.GoogleAccount, err error) error {
	if !errors.Is(err, google.ErrRevoked) {
		return err
	}
	if serr := e.store.SetGoogleAccountStatus(ctx, acct.ID, domain.GoogleRevoked, scrub(err)); serr != nil {
		return errors.Join(err, serr)
	}
	e.logger.LogAttrs(ctx, slog.LevelWarn, "google access revoked, sync paused", slog.String("user", acct.UserID.String()))
	return jobs.Permanent(err)
}

func (e *Engine) observe(entity, dir string, n int, err error) {
	if e.metrics == nil {
		return
	}
	e.metrics.SyncRuns.WithLabelValues(entity, metrics.Result(err)).Inc()
	e.metrics.SyncItems.WithLabelValues(entity, dir, "apply").Add(float64(n))
}

func (e *Engine) account(ctx context.Context, owner domain.ID) (domain.GoogleAccount, error) {
	acct, err := e.store.GoogleAccount(ctx, owner)
	if errors.Is(err, domain.ErrNotFound) {
		return acct, ErrNotConnected
	}
	return acct, err
}

func (e *Engine) load(ctx context.Context, id domain.ID) (domain.SyncBinding, domain.GoogleAccount, bool, error) {
	b, err := e.store.Binding(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return b, domain.GoogleAccount{}, false, nil
	}
	if err != nil {
		return b, domain.GoogleAccount{}, false, err
	}
	acct, err := e.store.GoogleAccountByID(ctx, b.AccountID)
	if errors.Is(err, domain.ErrNotFound) {
		return b, acct, false, nil
	}
	if err != nil {
		return b, acct, false, err
	}
	return b, acct, b.Enabled && acct.Active(), nil
}

func (e *Engine) pollAfter(b domain.SyncBinding) time.Duration {
	if b.Watching(e.now()) {
		return watchedPoll
	}
	return e.poll
}

func (e *Engine) queuePush(ctx context.Context, b domain.SyncBinding) error {
	if !b.Pushes() {
		return nil
	}
	at, err := e.store.NextOutboxAt(ctx, b.ID)
	if err != nil || at == nil {
		return err
	}
	return e.queue(ctx, jobs.KindGooglePush, b.ID, *at)
}

func (e *Engine) Connect(ctx context.Context, user domain.ID, identity domain.Identity, refresh string, scopes []string) error {
	_, err := e.store.SaveGoogleAccount(ctx, domain.GoogleAccount{
		ID:              domain.NewID(),
		UserID:          user,
		IdentityID:      identity.ID,
		RefreshTokenEnc: e.keys.SealString(refresh, refreshAAD(user)),
		Scopes:          scopes,
	})
	if err != nil {
		return err
	}
	return e.Run(ctx, user)
}

func (e *Engine) Run(ctx context.Context, owner domain.ID) error {
	bs, err := e.store.Bindings(ctx, owner)
	if err != nil {
		return err
	}
	for _, b := range bs {
		if !b.Enabled {
			continue
		}
		if err := e.queue(ctx, jobs.KindGooglePull, b.ID, time.Time{}); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) Status(ctx context.Context, owner domain.ID) (Status, error) {
	var st Status
	acct, err := e.store.GoogleAccount(ctx, owner)
	if errors.Is(err, domain.ErrNotFound) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	st.Account = &acct
	bs, err := e.store.Bindings(ctx, owner)
	if err != nil {
		return st, err
	}
	counts, err := e.store.OutboxCounts(ctx, owner)
	if err != nil {
		return st, err
	}
	now := e.now()
	for _, b := range bs {
		st.Bindings = append(st.Bindings, BindingStatus{SyncBinding: b, Pending: counts[b.ID], Watching: b.Watching(now)})
	}
	return st, nil
}

func (e *Engine) Banner(ctx context.Context, user domain.ID) string {
	acct, err := e.store.GoogleAccount(ctx, user)
	if err != nil || acct.Active() {
		return ""
	}
	return "Google access was lost, so sync is paused."
}

func (e *Engine) Remotes(ctx context.Context, owner domain.ID) (Remotes, error) {
	acct, err := e.account(ctx, owner)
	if err != nil {
		return Remotes{}, err
	}
	r, err := e.remote(ctx, acct)
	if err != nil {
		return Remotes{}, err
	}
	rem, err := fetchRemotes(ctx, r)
	if err != nil {
		return Remotes{}, e.failed(ctx, acct, err)
	}
	return rem, nil
}

func fetchRemotes(ctx context.Context, r google.Remote) (Remotes, error) {
	cals, err := r.Calendars(ctx)
	if err != nil {
		return Remotes{}, err
	}
	lists, err := r.TaskLists(ctx)
	return Remotes{Calendars: cals, Lists: lists}, err
}

func (rem Remotes) find(entity, id string) (source, bool) {
	switch entity {
	case domain.BindCalendar:
		for _, c := range rem.Calendars {
			if c.ID == id {
				return source{entity: entity, id: id, name: c.Name, color: c.Color, tz: c.TimeZone, readOnly: c.ReadOnly()}, true
			}
		}
	case domain.BindTaskList:
		for _, l := range rem.Lists {
			if l.ID == id {
				return source{entity: entity, id: id, name: l.Name}, true
			}
		}
	}
	return source{}, false
}

func (e *Engine) Apply(ctx context.Context, owner domain.ID, choices []Choice) error {
	acct, err := e.account(ctx, owner)
	if err != nil {
		return err
	}
	r, err := e.remote(ctx, acct)
	if err != nil {
		return err
	}
	rem, err := fetchRemotes(ctx, r)
	if err != nil {
		return e.failed(ctx, acct, err)
	}
	bindings, err := e.store.Bindings(ctx, owner)
	if err != nil {
		return err
	}
	current := make(map[string]domain.SyncBinding, len(bindings))
	for _, b := range bindings {
		current[b.Entity+"\x00"+b.RemoteID] = b
	}
	var v domain.ValidationError
	for _, c := range choices {
		cur, bound := current[c.Entity+"\x00"+c.RemoteID]
		if c.Target == "" {
			if bound {
				if err := e.unbind(ctx, r, cur); err != nil {
					return err
				}
			}
			continue
		}
		if bound && c.Target == cur.LocalID().String() && (c.Direction == "" || c.Direction == cur.Direction) {
			continue
		}
		src, ok := rem.find(c.Entity, c.RemoteID)
		if !ok {
			v.Add("target", "a Google calendar or list is no longer available, reload the page")
			continue
		}
		dir := c.Direction
		if !slices.Contains(domain.SyncDirections, dir) {
			dir = domain.SyncBoth
		}
		if src.readOnly {
			dir = domain.SyncPull
		}
		var err error
		switch {
		case bound && c.Target == cur.LocalID().String():
			if dir != cur.Direction {
				err = e.redirect(ctx, cur, dir)
			}
		default:
			if bound {
				if err := e.unbind(ctx, r, cur); err != nil {
					return err
				}
			}
			err = e.bind(ctx, r, acct, src, c.Target, dir)
		}
		var ve *domain.ValidationError
		switch {
		case errors.As(err, &ve):
			v.Fields = append(v.Fields, ve.Fields...)
		case err != nil:
			return err
		}
	}
	return v.Err()
}

func (e *Engine) bind(ctx context.Context, r google.Remote, acct domain.GoogleAccount, src source, target, dir string) error {
	var v domain.ValidationError
	owner := acct.UserID
	b := domain.SyncBinding{
		ID: domain.NewID(), OwnerID: owner, AccountID: acct.ID, Entity: src.entity, RemoteID: src.id,
		RemoteName: clip(src.name, 200), RemoteAccess: "owner", Direction: dir, Enabled: true,
	}
	if src.readOnly {
		b.RemoteAccess = "reader"
	}
	seed := false
	label := src.name
	if target == TargetNew {
		id, err := e.createLocal(ctx, owner, src)
		if err != nil {
			return err
		}
		if src.entity == domain.BindCalendar {
			b.CalendarID = &id
		} else {
			b.ListID = &id
		}
	} else {
		id, err := domain.ParseID(target)
		if err != nil {
			v.Add("target", "choose where to sync "+src.name)
			return v.Err()
		}
		if src.entity == domain.BindCalendar {
			c, err := e.store.Calendar(ctx, owner, id)
			switch {
			case errors.Is(err, domain.ErrNotFound):
				v.Add("target", "choose where to sync "+src.name)
				return v.Err()
			case err != nil:
				return err
			case c.ReadOnly && dir != domain.SyncPull:
				v.Add("target", c.Name+" is read only, sync it from Google only")
				return v.Err()
			}
			b.CalendarID, label = &id, c.Name
		} else {
			l, err := e.store.TodoList(ctx, owner, id)
			if errors.Is(err, domain.ErrNotFound) {
				v.Add("target", "choose where to sync "+src.name)
				return v.Err()
			}
			if err != nil {
				return err
			}
			b.ListID, label = &id, l.Name
		}
		seed = dir != domain.SyncPull
	}
	b, err := e.store.CreateBinding(ctx, b)
	if errors.Is(err, domain.ErrConflict) {
		v.Add("target", label+" is already synced with another Google calendar or list")
		return v.Err()
	}
	if err != nil {
		return err
	}
	if seed {
		if err := e.store.SeedOutbox(ctx, b); err != nil {
			return err
		}
	}
	if err := e.queue(ctx, jobs.KindGooglePull, b.ID, time.Time{}); err != nil {
		return err
	}
	if err := e.watch(ctx, r, b); err != nil {
		e.logger.LogAttrs(ctx, slog.LevelWarn, "google watch failed", slog.String("binding", b.ID.String()), slog.Any("err", err))
	}
	return nil
}

func (e *Engine) createLocal(ctx context.Context, owner domain.ID, src source) (domain.ID, error) {
	var v domain.ValidationError
	name := strings.TrimSpace(clip(src.name, 100))
	if name == "" {
		name = "Google"
	}
	if src.entity == domain.BindCalendar {
		n, err := e.store.CountCalendars(ctx, owner)
		if err != nil {
			return domain.NilID, err
		}
		if n >= domain.MaxCalendars {
			v.Addf("target", "an account can have at most %d calendars", domain.MaxCalendars)
			return domain.NilID, v.Err()
		}
		color, ok := domain.NormalizeColor(src.color)
		if !ok {
			color = domain.DefaultCalendarColor
		}
		tz := ""
		if domain.ValidTimezone(src.tz) {
			tz = src.tz
		}
		c, err := e.store.CreateCalendar(ctx, domain.Calendar{
			ID: domain.NewID(), OwnerID: owner, Name: name, Color: color, Timezone: tz, Kind: domain.CalendarLocal,
			ReadOnly: src.readOnly, Position: -1, DefaultReminders: []int{},
		})
		return c.ID, err
	}
	n, err := e.store.CountTodoLists(ctx, owner)
	if err != nil {
		return domain.NilID, err
	}
	if n >= domain.MaxTodoLists {
		v.Addf("target", "an account can have at most %d lists", domain.MaxTodoLists)
		return domain.NilID, v.Err()
	}
	l, err := e.store.CreateTodoList(ctx, domain.TodoList{ID: domain.NewID(), OwnerID: owner, Name: name, Color: domain.DefaultListColor, Position: -1})
	return l.ID, err
}

func (e *Engine) unbind(ctx context.Context, r google.Remote, cur domain.SyncBinding) error {
	if cur.WatchChannelID != "" && r != nil {
		if err := r.StopWatch(ctx, cur.WatchChannelID, cur.WatchResourceID); err != nil && !gone(err) {
			e.logger.LogAttrs(ctx, slog.LevelWarn, "google stop watch failed", slog.String("binding", cur.ID.String()), slog.Any("err", err))
		}
	}
	_, err := e.store.DeleteBinding(ctx, cur.OwnerID, cur.ID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil
	}
	return err
}

func (e *Engine) redirect(ctx context.Context, cur domain.SyncBinding, dir string) error {
	b, err := e.store.UpdateBinding(ctx, cur.OwnerID, cur.ID, dir, true)
	if err != nil {
		return err
	}
	if b.Pushes() && !cur.Pushes() {
		if err := e.store.SeedOutbox(ctx, b); err != nil {
			return err
		}
	}
	if b.Pulls() && !cur.Pulls() {
		b.SyncToken, b.UpdatedMin = "", nil
		if err := e.store.SaveBindingState(ctx, b); err != nil {
			return err
		}
	}
	return e.queue(ctx, jobs.KindGooglePull, b.ID, time.Time{})
}

func (e *Engine) Disconnect(ctx context.Context, owner domain.ID, keep bool) error {
	acct, err := e.account(ctx, owner)
	if err != nil {
		return err
	}
	bindings, err := e.store.Bindings(ctx, owner)
	if err != nil {
		return err
	}
	if rt, oerr := e.keys.OpenString(acct.RefreshTokenEnc, refreshAAD(owner)); oerr == nil {
		if r, derr := e.dial(ctx, rt); derr == nil {
			for _, b := range bindings {
				if b.WatchChannelID != "" {
					_ = r.StopWatch(ctx, b.WatchChannelID, b.WatchResourceID)
				}
			}
		}
		if e.oauth != nil {
			if rerr := e.oauth.Revoke(ctx, rt); rerr != nil {
				e.logger.LogAttrs(ctx, slog.LevelWarn, "google token revoke failed", slog.Any("err", rerr))
			}
		}
	}
	if err := e.store.DeleteGoogleAccount(ctx, owner); err != nil {
		return err
	}
	if keep {
		return nil
	}
	for _, b := range bindings {
		var derr error
		switch {
		case b.CalendarID != nil:
			derr = e.store.DeleteCalendar(ctx, owner, *b.CalendarID, func(c domain.Calendar) error {
				if c.IsDefault {
					return errKeepLocal
				}
				return nil
			})
		case b.ListID != nil:
			derr = e.store.DeleteTodoList(ctx, owner, *b.ListID, func(l domain.TodoList) error {
				if l.IsDefault {
					return errKeepLocal
				}
				return nil
			})
		}
		if derr != nil && !errors.Is(derr, errKeepLocal) && !errors.Is(derr, domain.ErrNotFound) {
			return derr
		}
	}
	return nil
}

func (e *Engine) HandlePoll(ctx context.Context, _ jobs.Job) error {
	ids, err := e.store.DueBindings(ctx, e.now(), pollBatch)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := e.queue(ctx, jobs.KindGooglePull, id, time.Time{}); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) HandleWatch(ctx context.Context, _ jobs.Job) error {
	if e.webhook == "" {
		return nil
	}
	bs, err := e.store.WatchCandidates(ctx, e.now().Add(renewBefore))
	if err != nil {
		return err
	}
	remotes := map[domain.ID]google.Remote{}
	for _, b := range bs {
		r, ok := remotes[b.AccountID]
		if !ok {
			acct, err := e.store.GoogleAccountByID(ctx, b.AccountID)
			if err != nil {
				return err
			}
			if r, err = e.remote(ctx, acct); err != nil {
				e.logger.LogAttrs(ctx, slog.LevelWarn, "google watch skipped", slog.String("binding", b.ID.String()), slog.Any("err", err))
				continue
			}
			remotes[b.AccountID] = r
		}
		if err := e.watch(ctx, r, b); err != nil {
			e.logger.LogAttrs(ctx, slog.LevelWarn, "google watch failed", slog.String("binding", b.ID.String()), slog.Any("err", err))
		}
	}
	return nil
}

func (e *Engine) watch(ctx context.Context, r google.Remote, b domain.SyncBinding) error {
	if e.webhook == "" || b.Entity != domain.BindCalendar || !b.Pulls() {
		return nil
	}
	if b.WatchChannelID != "" {
		if err := r.StopWatch(ctx, b.WatchChannelID, b.WatchResourceID); err != nil && !gone(err) {
			e.logger.LogAttrs(ctx, slog.LevelWarn, "google stop watch failed", slog.String("binding", b.ID.String()), slog.Any("err", err))
		}
	}
	channel := domain.NewID().String()
	token := crypto.RandomToken(32)
	resource, exp, err := r.Watch(ctx, b.RemoteID, channel, token, e.webhook)
	if err != nil {
		return errors.Join(err, e.store.SaveWatch(ctx, b.ID, "", "", nil, nil))
	}
	return e.store.SaveWatch(ctx, b.ID, channel, resource, crypto.HashToken(token), &exp)
}

func (e *Engine) Webhook(w http.ResponseWriter, r *http.Request) {
	channel := r.Header.Get("X-Goog-Channel-ID")
	if channel == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	b, err := e.store.BindingByChannel(ctx, channel)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		w.WriteHeader(http.StatusNoContent)
		return
	case err != nil:
		e.logger.LogAttrs(ctx, slog.LevelError, "google webhook lookup failed", slog.Any("err", err))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if !crypto.Equal(crypto.HashToken(r.Header.Get("X-Goog-Channel-Token")), b.WatchTokenHash) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if r.Header.Get("X-Goog-Resource-State") != "sync" {
		if err := e.queue(ctx, jobs.KindGooglePull, b.ID, e.now().Add(webhookDelay)); err != nil {
			e.logger.LogAttrs(ctx, slog.LevelError, "google webhook enqueue failed", slog.Any("err", err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
