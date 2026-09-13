package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/Alexander-D-Karpov/calendar/internal/httpx"
)

func TestMiddlewareLabelsByRoute(t *testing.T) {
	m := New()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /cal/{view}/{date}", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("POST /api/v1/events", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	h := httpx.Chain(mux, httpx.Route(mux), m.Middleware)

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/cal/week/2026-09-10", nil),
		httptest.NewRequest(http.MethodGet, "/cal/month/2026-09-01", nil),
		httptest.NewRequest(http.MethodPost, "/api/v1/events", nil),
		httptest.NewRequest(http.MethodGet, "/s/secrettoken", nil),
		httptest.NewRequest("PROPFIND", "/cal/week/2026-09-10", nil),
	} {
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	cases := []struct {
		method, route, code string
		want                float64
	}{
		{"GET", "GET /cal/{view}/{date}", "200", 2},
		{"POST", "POST /api/v1/events", "201", 1},
		{"GET", httpx.Unmatched, "404", 1},
		{"OTHER", httpx.Unmatched, "405", 1},
	}
	for _, c := range cases {
		if got := testutil.ToFloat64(m.httpRequests.WithLabelValues(c.method, c.route, c.code)); got != c.want {
			t.Errorf("%s %s %s = %v, want %v", c.method, c.route, c.code, got, c.want)
		}
	}
	if got := testutil.ToFloat64(m.httpInFlight); got != 0 {
		t.Errorf("in flight = %v", got)
	}
}

func TestHandlerExposesMetrics(t *testing.T) {
	m := New()
	m.Jobs.WithLabelValues("sync", Result(nil)).Inc()
	srv := httptest.NewServer(m.Server("", "").Handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{
		"calendar_build_info{",
		`calendar_jobs_total{kind="sync",result="ok"} 1`,
		"go_goroutines",
		"calendar_http_requests_in_flight",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("metrics output missing %q", want)
		}
	}
}

func TestMetricsBearerToken(t *testing.T) {
	srv := httptest.NewServer(New().Server("", "scrape-token-0123456789").Handler)
	defer srv.Close()

	cases := map[string]int{
		"":                               http.StatusUnauthorized,
		"Bearer wrong":                   http.StatusUnauthorized,
		"Basic scrape-token-0123456789":  http.StatusUnauthorized,
		"Bearer scrape-token-0123456789": http.StatusOK,
		"bearer scrape-token-0123456789": http.StatusOK,
	}
	for auth, want := range cases {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/metrics", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("Authorization %q = %d, want %d", auth, resp.StatusCode, want)
		}
	}
}
