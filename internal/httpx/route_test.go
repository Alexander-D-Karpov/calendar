package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRouteCapturesPattern(t *testing.T) {
	var inner, outer string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /cal/{view}/{date}", func(w http.ResponseWriter, r *http.Request) {
		inner = RouteFrom(r.Context())
	})
	h := Route(mux)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r)
		outer = RouteLabel(r.Context())
	}))

	serve(h, httptest.NewRequest(http.MethodGet, "/cal/week/2026-09-10", nil))
	if inner != "GET /cal/{view}/{date}" || outer != inner {
		t.Fatalf("inner = %q, outer = %q", inner, outer)
	}

	serve(h, httptest.NewRequest(http.MethodGet, "/s/secrettoken", nil))
	if outer != Unmatched {
		t.Fatalf("unmatched route = %q", outer)
	}
}

func TestSetRouteAndRecord(t *testing.T) {
	var label string
	var info ResponseInfo
	mux := http.NewServeMux()
	h := Route(mux)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetRoute(r.Context(), "GET /s/{token}/ws")
		var rw http.ResponseWriter
		rw, info = Record(w)
		rw.WriteHeader(http.StatusAccepted)
		_, _ = rw.Write([]byte("abc"))
		label = RouteLabel(r.Context())
	}))
	serve(h, httptest.NewRequest(http.MethodGet, "/x", nil))
	if label != "GET /s/{token}/ws" {
		t.Fatalf("label = %q", label)
	}
	if info.StatusCode() != http.StatusAccepted || info.Size() != 3 || info.Hijacked() {
		t.Fatalf("info = %d %d %v", info.StatusCode(), info.Size(), info.Hijacked())
	}
	if RouteLabel(httptest.NewRequest(http.MethodGet, "/", nil).Context()) != Unmatched {
		t.Fatal("missing route must be unmatched")
	}
}
