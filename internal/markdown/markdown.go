package markdown

import (
	"bytes"
	"html/template"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

const plainScan = 4000

var (
	engine = goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(html.WithHardWraps()),
	)
	policy      = newPolicy()
	linkPattern = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	markPattern = regexp.MustCompile("[*_`#>~|]+")
)

func newPolicy() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.RequireNoReferrerOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)
	return p
}

func Render(src string) template.HTML {
	if strings.TrimSpace(src) == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := engine.Convert([]byte(src), &buf); err != nil {
		return template.HTML("<p>" + template.HTMLEscapeString(src) + "</p>")
	}
	return template.HTML(policy.SanitizeBytes(buf.Bytes()))
}

func Plain(src string, limit int) string {
	if len(src) > plainScan {
		src = strings.ToValidUTF8(src[:plainScan], "")
	}
	s := linkPattern.ReplaceAllString(src, "$1")
	s = markPattern.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:limit])) + "…"
}
