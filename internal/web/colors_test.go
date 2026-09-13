package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestColorsCSS(t *testing.T) {
	rec := httptest.NewRecorder()
	colorsCSS(rec, httptest.NewRequest(http.MethodGet, colorsHref([]string{"3b6ea5", "aa3355"}), nil))
	if rec.Code != 200 || rec.Body.String() != ".hex-3b6ea5{--c:#3b6ea5}\n.hex-aa3355{--c:#aa3355}\n" {
		t.Fatalf("css = %d %q", rec.Code, rec.Body)
	}
	for _, bad := range []string{"/colors.css?c=red", "/colors.css?c=3B6EA5", "/colors.css?c=3b6ea5;x", "/colors.css"} {
		rec := httptest.NewRecorder()
		colorsCSS(rec, httptest.NewRequest(http.MethodGet, bad, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s = %d", bad, rec.Code)
		}
	}
}
