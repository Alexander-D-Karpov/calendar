package shares

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/og"
	"github.com/Alexander-D-Karpov/calendar/internal/view"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
	"github.com/Alexander-D-Karpov/calendar/internal/web/timeline"
)

type StampStore interface {
	ShareStamp(ctx context.Context, owner domain.ID, cals []domain.ID, todos bool) (int64, error)
}

var group singleflight.Group

// ogVersion changes exactly when the rendered picture would, so the image URL
// can be cached forever and still never go stale.
func (m *Module) ogVersion(ctx context.Context, sh domain.Share) string {
	v := strconv.FormatInt(sh.Version, 36)
	if m.stamps == nil {
		return v
	}
	seq, err := m.stamps.ShareStamp(ctx, sh.OwnerID, sh.Calendars, sh.IncludeTodos)
	if err != nil {
		return v
	}
	return v + "-" + strconv.FormatInt(seq, 36)
}

func (m *Module) og(w http.ResponseWriter, r *http.Request) {
	if m.og_ == nil || m.cache == nil {
		m.site.Fail(w, r, domain.ErrNotFound)
		return
	}
	ctx := r.Context()
	// Open does not reveal the token, so the canonical URL has to be rebuilt
	// from the one the caller already used.
	token := r.PathValue("token")
	sh, err := m.shares.Open(ctx, token, false)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	version := m.ogVersion(ctx, sh)
	// Never serve one version's picture under another's URL: the caller was
	// told this address is immutable.
	if v := r.URL.Query().Get("v"); v != version {
		m.count("miss")
		http.Redirect(w, r, m.ogURL(token, version), http.StatusFound)
		return
	}
	key := og.Key(sh.ID.String(), version)
	if body, ok := m.cache.Get(key); ok {
		m.count("hit")
		m.writeOG(w, body)
		return
	}
	body, err, _ := group.Do(key, func() (any, error) {
		if body, ok := m.cache.Get(key); ok {
			return body, nil
		}
		b, err := m.renderOG(ctx, sh)
		if err != nil {
			return nil, err
		}
		m.cache.Put(key, b)
		return b, nil
	})
	if err != nil {
		m.count("error")
		m.site.Logger().LogAttrs(ctx, slog.LevelWarn, "og render failed", slog.Any("err", err))
		m.site.Fail(w, r, err)
		return
	}
	m.count("miss")
	m.writeOG(w, body.([]byte))
}

func (m *Module) renderOG(ctx context.Context, sh domain.Share) ([]byte, error) {
	start := time.Now()
	owner, err := m.site.Auth().User(ctx, sh.OwnerID)
	if err != nil {
		return nil, err
	}
	loc := owner.Location()
	p := sharePeriod(sh, owner)
	cals, err := m.cals.List(ctx, sh.OwnerID)
	if err != nil {
		return nil, err
	}
	res, err := m.src.Load(ctx, timeline.Query{
		Owner: owner, Loc: loc, Period: p, Calendars: cals, Selected: sh.Calendars,
		Todos: sh.IncludeTodos, Sleep: sh.ShowSleep,
	})
	if err != nil {
		return nil, err
	}
	items := view.Redact(res.Items, sh.Detail)
	opts := view.Options{
		Loc: loc, Now: m.site.Clock().Now(), Clock24: owner.TimeFormat != domain.TimeFormat12,
		WeekStart: time.Weekday(owner.WeekStart), Sleep: res.Sleep,
	}
	body, err := m.og_.Render(p, items, opts, og.Options{
		AppName: m.site.Config().App.Name,
		Name:    sh.Name,
		Title:   p.Title(),
		Kind:    p.Kind.Label(),
		TZ:      loc.String(),
		Counts:  countsOf(items),
	})
	if err != nil {
		return nil, err
	}
	if m.metrics != nil {
		m.metrics.OGRender.Observe(time.Since(start).Seconds())
	}
	return body, nil
}

func (m *Module) writeOG(w http.ResponseWriter, body []byte) {
	h := w.Header()
	h.Set("Content-Type", "image/png")
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("X-Robots-Tag", "noindex")
	_, _ = w.Write(body)
}

func (m *Module) count(result string) {
	if m.metrics != nil {
		m.metrics.OGRequests.WithLabelValues(result).Inc()
	}
}

func (m *Module) ogURL(token, version string) string {
	return m.site.Config().App.URL("/s/" + token + "/og.png?v=" + version)
}

func countsOf(items []view.Item) string {
	events, todos := 0, 0
	for _, it := range items {
		if it.Todo {
			todos++
			continue
		}
		events++
	}
	out := web.Plural(events, "event")
	if todos > 0 {
		out += ", " + web.Plural(todos, "todo")
	}
	return out
}
