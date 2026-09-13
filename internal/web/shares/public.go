package shares

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/httpx"
	"github.com/Alexander-D-Karpov/calendar/internal/ical"
	"github.com/Alexander-D-Karpov/calendar/internal/realtime"
	"github.com/Alexander-D-Karpov/calendar/internal/transfer"
	"github.com/Alexander-D-Karpov/calendar/internal/view"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
	"github.com/Alexander-D-Karpov/calendar/internal/web/timeline"
)

const viewerSlack = 14 * time.Hour

type publicView struct {
	view.Rendered
	Name     string
	Title    string
	Kind     string
	TZ       string
	ViewerTZ bool
	Colors   []string
	Notice   string
	Live     string
	Seq      int64
	OGImage  string
	OGDesc   string
}

func (m *Module) public(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	// ServeMux cannot tell "/s/{token}.ics" from "/s/{token}": the wildcard
	// swallows the suffix, so the split happens here.
	if name, ok := strings.CutSuffix(token, ".ics"); ok {
		m.feed(w, r, name)
		return
	}
	ctx := r.Context()
	sh, err := m.shares.Open(ctx, token, !web.IsFragment(r))
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	owner, err := m.site.Auth().User(ctx, sh.OwnerID)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	loc := owner.Location()
	if sh.TZMode == domain.ShareTZViewer {
		if tz := r.URL.Query().Get("tz"); domain.ValidTimezone(tz) {
			if l, err := time.LoadLocation(tz); err == nil {
				loc = l
			}
		}
	}
	p := sharePeriod(sh, owner)
	cals, err := m.cals.List(ctx, sh.OwnerID)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	res, err := m.src.Load(ctx, timeline.Query{
		Owner:     owner,
		Loc:       loc,
		Period:    p,
		Calendars: cals,
		Selected:  sh.Calendars,
		Todos:     sh.IncludeTodos,
		Sleep:     sh.ShowSleep,
	})
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	items := view.Redact(res.Items, sh.Detail)
	opts := view.Options{
		Loc:       loc,
		Now:       m.site.Clock().Now(),
		Clock24:   owner.TimeFormat != "12h",
		WeekStart: time.Weekday(owner.WeekStart),
		Sleep:     res.Sleep,
	}
	pv := publicView{
		Rendered: view.Render(p, items, opts),
		Name:     sh.Name,
		Title:    p.Title(),
		Kind:     p.Kind.Label(),
		TZ:       loc.String(),
		ViewerTZ: sh.TZMode == domain.ShareTZViewer,
		Colors:   view.Colors(items, nil),
		Notice:   res.Notice,
		Live:     "/s/" + token + "/ws",
		Seq:      res.Seq,
	}
	if m.og_ != nil {
		pv.OGImage = m.ogURL(token, m.ogVersion(ctx, sh))
		pv.OGDesc = p.Kind.Label() + " · " + p.Title() + " · " + countsOf(items)
	}
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	httpx.SetReferrerPolicy(w, "no-referrer")
	m.site.RenderBlock(w, r, http.StatusOK, "share", web.Block(r, "share_view"), &web.Page{Title: sh.Name, Data: pv})
}

func (m *Module) publicLive(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sh, err := m.shares.Open(ctx, r.PathValue("token"), false)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	owner, err := m.site.Auth().User(ctx, sh.OwnerID)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	from, to := sharePeriod(sh, owner).Range(owner.Location())
	since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
	m.hub.Serve(w, r, realtime.Stream{
		Kind:   "share",
		Owner:  sh.OwnerID,
		Since:  since,
		Filter: realtime.ShareFilter(sh.ID, sh.Calendars, sh.IncludeTodos, from.Add(-viewerSlack), to.Add(viewerSlack)),
		Replay: m.src.Changes.ChangesSince,
	})
}

func sharePeriod(sh domain.Share, owner domain.User) view.Period {
	k, ok := view.ParseKind(sh.View)
	if !ok {
		k = view.Week
	}
	return view.NewPeriod(k, sh.Period, time.Weekday(owner.WeekStart))
}

// feed serves the share as a subscribable calendar. A busy-detail share must
// not leak titles, so the redaction happens before encoding rather than in the
// view layer that the HTML page uses.
func (m *Module) feed(w http.ResponseWriter, r *http.Request, token string) {
	ctx := r.Context()
	sh, err := m.shares.Open(ctx, token, true)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	owner, err := m.site.Auth().User(ctx, sh.OwnerID)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	p := sharePeriod(sh, owner)
	from, to := p.Range(owner.Location())
	set, err := m.export.Range(ctx, sh.OwnerID, sh.Calendars, from, to, sh.IncludeTodos)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	if sh.Detail == domain.DetailBusy {
		redactSet(&set)
	}
	body, err := ical.Encode(set, sh.Name)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/calendar; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Robots-Tag", "noindex")
	h.Set("Content-Disposition", "inline")
	httpx.SetReferrerPolicy(w, "no-referrer")
	_, _ = w.Write(body)
}

func redactSet(set *transfer.Set) {
	for i := range set.Calendars {
		for j := range set.Calendars[i].Series {
			s := &set.Calendars[i].Series[j]
			blank(&s.Master)
			for k := range s.Overrides {
				blank(&s.Overrides[k])
			}
		}
	}
	set.Lists = nil
}

func blank(e *domain.Event) {
	e.Title, e.Body, e.Location, e.URL = "Busy", "", "", ""
}
