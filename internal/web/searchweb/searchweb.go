package searchweb

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/Alexander-D-Karpov/calendar/internal/search"
	"github.com/Alexander-D-Karpov/calendar/internal/view"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

const path = "/search"

type Deps struct {
	Site   *web.Server
	Search *search.Service
}

type Module struct {
	site   *web.Server
	search *search.Service
}

func New(d Deps) *Module {
	return &Module{site: d.Site, search: d.Search}
}

func (m *Module) Routes(mux *http.ServeMux) {
	h, user := m.site.Handle, m.site.RequireUser
	mux.Handle("GET "+path, h(m.page, user))
}

var kinds = []web.Option{
	{Value: search.TypeAny, Label: "Everything"},
	{Value: search.TypeEvent, Label: "Events"},
	{Value: search.TypeTodo, Label: "Todos"},
}

type row struct {
	search.Hit
	When string
}

type pageView struct {
	Raw      string
	Type     string
	Kinds    []web.Option
	Rows     []row
	Colors   []string
	Warnings []string
	Empty    bool
	More     bool
	NextHref string
	PrevHref string
	Count    int
	Offset   int
}

func (m *Module) page(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	q := r.URL.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	raw, kind := q.Get("q"), q.Get("type")
	res, err := m.search.Search(r.Context(), v.User.ID, search.Request{Raw: raw, Type: kind, Offset: offset})
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	pv := pageView{
		Raw: raw, Type: res.Query.Type, Kinds: kinds, Warnings: res.Warnings,
		Empty: res.Query.Empty(), More: res.More, Count: len(res.Hits), Offset: res.Offset,
	}
	for _, h := range res.Hits {
		pv.Rows = append(pv.Rows, row{Hit: h, When: h.When(v.Loc, v.Clock24())})
	}
	if res.More {
		pv.NextHref = link(raw, pv.Type, res.Next())
	}
	if res.Offset > 0 {
		pv.PrevHref = link(raw, pv.Type, res.Prev())
	}
	colors := []string{}
	seen := map[string]bool{}
	for _, h := range res.Hits {
		if hex := view.Hex(h.Color); hex != "" && !seen[hex] {
			seen[hex] = true
			colors = append(colors, hex)
		}
	}
	if pv.Type == "none" {
		pv.Type = search.TypeAny
	}
	pv.Colors = colors
	m.site.Render(w, r, http.StatusOK, "search", v.Page("search", title(raw), pv))
}

func title(raw string) string {
	if raw == "" {
		return "Search"
	}
	return "Search: " + raw
}

func link(raw, kind string, offset int) string {
	v := url.Values{"q": {raw}}
	if kind != "" {
		v.Set("type", kind)
	}
	if offset > 0 {
		v.Set("offset", strconv.Itoa(offset))
	}
	return path + "?" + v.Encode()
}
