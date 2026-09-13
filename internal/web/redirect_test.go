package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Alexander-D-Karpov/calendar/internal/config"
)

var offsiteTargets = []string{
	"//evil.example",
	"https://evil.example/",
	"/\\evil.example",
	"/\t/evil.example",
	"/path\r\nLocation: https://evil.example",
}

// Redirect is exported and every caller passes a path it built itself, so the
// guard has to live in the helper: a caller that forgets it must not be able to
// send a visitor off-site.
func TestRedirectRefusesOffsiteTargets(t *testing.T) {
	s := &Server{cfg: &config.Config{}}
	for _, target := range offsiteTargets {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		s.Redirect(w, r, target)
		if got := w.Header().Get("Location"); got != "/" {
			t.Errorf("Redirect(%q) sent visitor to %q, want %q", target, got, "/")
		}
	}
}

// The fragment path never issues a 303, it hands the client a target in a
// header instead, so it needs the same guard.
func TestFinishRefusesOffsiteTargetsInFragments(t *testing.T) {
	s := &Server{cfg: &config.Config{}}
	for _, target := range offsiteTargets {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set(FragmentHeader, "1")
		s.Finish(w, r, target)
		if got := w.Header().Get("X-Location"); got != "/" {
			t.Errorf("Finish(%q) set X-Location %q, want %q", target, got, "/")
		}
	}
}

func TestRedirectKeepsOrdinaryPaths(t *testing.T) {
	paths := []string{"/", "/settings/security", "/login?next=%2Ftodos", "/c/day/2026-09-13", "/todos/#done"}
	s := &Server{cfg: &config.Config{}}
	for _, target := range paths {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		s.Redirect(w, r, target)
		if got := w.Header().Get("Location"); got != target {
			t.Errorf("Redirect(%q) rewrote the target to %q", target, got)
		}
	}
}
