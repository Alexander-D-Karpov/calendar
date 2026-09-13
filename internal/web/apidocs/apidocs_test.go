package apidocs

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

func TestDocsPage(t *testing.T) {
	r, err := web.EmbeddedRenderer()
	if err != nil {
		t.Fatal(err)
	}
	for _, vendored := range []bool{true, false} {
		p := &web.Page{
			Title:  "API documentation",
			Nav:    "api",
			CSRF:   "csrf-value",
			Theme:  "dark",
			Form:   web.NewForm(nil),
			Footer: &web.Footer{AppName: "Calendar", GoVersion: "go1.27.0"},
			Data:   docsView{Vendored: vendored},
		}
		var buf bytes.Buffer
		if err := r.Execute(&buf, "api_docs", p); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		if strings.Contains(out, `id="swagger-ui"`) != vendored {
			t.Errorf("vendored=%v: swagger mount mismatch", vendored)
		}
		if strings.Contains(out, "make vendor-swagger") == vendored {
			t.Errorf("vendored=%v: fallback message mismatch", vendored)
		}
		for _, want := range []string{`href="/api/openapi.yaml"`, `href="/api/openapi.json"`, "Page: ", `name="csrf-token"`} {
			if !strings.Contains(out, want) {
				t.Errorf("vendored=%v: missing %q", vendored, want)
			}
		}
		if vendored && !strings.Contains(out, "/static/js/api-docs.js") {
			t.Error("docs page must load api-docs.js")
		}
	}
}

func TestDocsCSP(t *testing.T) {
	directives := map[string]string{}
	for _, d := range strings.Split(docsCSP, "; ") {
		name, value, _ := strings.Cut(d, " ")
		directives[name] = value
	}
	if directives["script-src"] != "'self'" {
		t.Errorf("script-src = %q", directives["script-src"])
	}
	if directives["frame-ancestors"] != "'none'" || directives["connect-src"] != "'self'" {
		t.Errorf("directives = %v", directives)
	}
	if strings.Contains(docsCSP, "unsafe-eval") {
		t.Error("docs CSP must not allow eval")
	}
}
