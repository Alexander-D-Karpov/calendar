package web

import (
	"io"
	"net/http"
	"regexp"
	"strings"
)

const maxColors = 128

var hexPattern = regexp.MustCompile(`^[0-9a-f]{6}$`)

func colorsCSS(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Query().Get("c"), ",")
	if len(parts) > maxColors {
		http.Error(w, "too many colors", http.StatusBadRequest)
		return
	}
	var b strings.Builder
	for _, c := range parts {
		if !hexPattern.MatchString(c) {
			http.Error(w, "invalid color", http.StatusBadRequest)
			return
		}
		b.WriteString(".hex-" + c + "{--c:#" + c + "}\n")
	}
	h := w.Header()
	h.Set("Content-Type", "text/css; charset=utf-8")
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = io.WriteString(w, b.String())
}

func colorsHref(colors []string) string {
	return "/colors.css?c=" + strings.Join(colors, ",")
}
