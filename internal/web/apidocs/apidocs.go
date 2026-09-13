package apidocs

import (
	"net/http"
	"strings"

	"github.com/Alexander-D-Karpov/calendar/internal/httpx"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

const (
	swaggerJS  = "vendor/swagger-ui/swagger-ui-bundle.js"
	swaggerCSS = "vendor/swagger-ui/swagger-ui.css"
)

var docsCSP = strings.Join([]string{
	"default-src 'self'",
	"script-src 'self'",
	"style-src 'self' 'unsafe-inline'",
	"img-src 'self' data: blob:",
	"font-src 'self'",
	"connect-src 'self'",
	"worker-src 'none'",
	"form-action 'self'",
	"frame-ancestors 'none'",
	"base-uri 'none'",
	"object-src 'none'",
}, "; ")

type docsView struct {
	Vendored bool
}

type Module struct {
	site *web.Server
}

func New(site *web.Server) *Module {
	return &Module{site: site}
}

func (m *Module) Routes(mux *http.ServeMux) {
	mux.Handle("GET /api/docs", http.RedirectHandler("/api/docs/", http.StatusMovedPermanently))
	mux.Handle("GET /api/docs/{$}", m.site.Handle(m.page))
}

func (m *Module) page(w http.ResponseWriter, r *http.Request) {
	httpx.SetCSP(w, docsCSP)
	m.site.Render(w, r, http.StatusOK, "api_docs", &web.Page{
		Title: "API documentation",
		Nav:   "api",
		Data:  docsView{Vendored: m.site.HasAsset(swaggerJS) && m.site.HasAsset(swaggerCSS)},
	})
}
