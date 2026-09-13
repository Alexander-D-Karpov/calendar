package metrics

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Alexander-D-Karpov/calendar/internal/buildinfo"
	"github.com/Alexander-D-Karpov/calendar/internal/httpx"
)

const namespace = "calendar"

var durationBuckets = []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30}

type Metrics struct {
	registry *prometheus.Registry

	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec
	httpInFlight prometheus.Gauge

	WSConnections         *prometheus.GaugeVec
	RealtimeEvents        *prometheus.CounterVec
	Jobs                  *prometheus.CounterVec
	JobDuration           *prometheus.HistogramVec
	JobQueue              *prometheus.GaugeVec
	SyncRuns              *prometheus.CounterVec
	SyncItems             *prometheus.CounterVec
	SubscriptionRefreshes *prometheus.CounterVec
	Imports               *prometheus.CounterVec
	PushSent              *prometheus.CounterVec
	Mail                  *prometheus.CounterVec
	Reminders             *prometheus.CounterVec
	ReminderLag           prometheus.Histogram
	OGRequests            *prometheus.CounterVec
	OGRender              prometheus.Histogram
	OGCacheBytes          prometheus.Gauge
}

func New() *Metrics {
	m := &Metrics{registry: prometheus.NewRegistry()}
	f := factory{m.registry}

	m.httpRequests = f.counter("http_requests_total", "HTTP requests by route and status code.", "method", "route", "code")
	m.httpDuration = f.histogram("http_request_duration_seconds", "HTTP request latency by route.", durationBuckets, "method", "route")
	m.httpInFlight = f.gauge("http_requests_in_flight", "HTTP requests currently being served.")

	m.WSConnections = f.gaugeVec("websocket_connections", "Open WebSocket connections.", "kind")
	m.RealtimeEvents = f.counter("realtime_events_total", "Realtime messages sent to clients.", "kind")
	m.Jobs = f.counter("jobs_total", "Background jobs finished by result.", "kind", "result")
	m.JobDuration = f.histogram("job_duration_seconds", "Background job run time.", durationBuckets, "kind")
	m.JobQueue = f.gaugeVec("jobs", "Jobs in the queue by status.", "status")
	m.SyncRuns = f.counter("google_sync_runs_total", "Google sync runs by entity and result.", "entity", "result")
	m.SyncItems = f.counter("google_sync_items_total", "Items synced with Google.", "entity", "direction", "op")
	m.SubscriptionRefreshes = f.counter("subscription_refreshes_total", "ICS subscription refreshes by result.", "result")
	m.Imports = f.counter("imports_total", "Imports by source and result.", "source", "result")
	m.PushSent = f.counter("push_sent_total", "Web Push deliveries by result.", "result")
	m.Mail = f.counter("mail_sent_total", "Emails sent by kind and result.", "kind", "result")
	m.Reminders = f.counter("reminders_delivered_total", "Reminders delivered by entity and channel.", "entity", "channel")
	m.ReminderLag = f.histogramOne("reminder_lag_seconds", "Delay between reminder fire time and delivery.", []float64{1, 5, 15, 30, 60, 120, 300, 900})
	m.OGRequests = f.counter("og_requests_total", "OG image requests by cache result.", "result")
	m.OGRender = f.histogramOne("og_render_seconds", "OG image render time.", durationBuckets)
	m.OGCacheBytes = f.gauge("og_cache_bytes", "OG cache size on disk.")

	info := buildinfo.Get()
	build := f.gaugeVec("build_info", "Build information.", "version", "commit", "go_version")
	build.WithLabelValues(info.Version, info.ShortCommit(), info.GoVersion).Set(1)

	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return m
}

func (m *Metrics) Register(c prometheus.Collector) error {
	return m.registry.Register(c)
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		Registry:          m.registry,
		EnableOpenMetrics: true,
		Timeout:           10 * time.Second,
	})
}

func (m *Metrics) Server(addr, token string) *http.Server {
	var h http.Handler = m.Handler()
	if token != "" {
		h = requireBearer(token, h)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", h)
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func requireBearer(token string, next http.Handler) http.Handler {
	want := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, got, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		sum := sha256.Sum256([]byte(strings.TrimSpace(got)))
		if !ok || !strings.EqualFold(scheme, "Bearer") || subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="metrics"`)
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.httpInFlight.Inc()
		start := time.Now()
		rw, info := httpx.Record(w)
		defer func() {
			m.httpInFlight.Dec()
			method := normalizeMethod(r.Method)
			route := httpx.RouteLabel(r.Context())
			m.httpRequests.WithLabelValues(method, route, strconv.Itoa(info.StatusCode())).Inc()
			if !info.Hijacked() {
				m.httpDuration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
			}
		}()
		next.ServeHTTP(rw, r)
	})
}

func Result(err error) string {
	if err != nil {
		return "error"
	}
	return "ok"
}

func normalizeMethod(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return m
	}
	return "OTHER"
}

type factory struct {
	reg *prometheus.Registry
}

func (f factory) counter(name, help string, labels ...string) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: namespace, Name: name, Help: help}, labels)
	f.reg.MustRegister(c)
	return c
}

func (f factory) gauge(name, help string) prometheus.Gauge {
	g := prometheus.NewGauge(prometheus.GaugeOpts{Namespace: namespace, Name: name, Help: help})
	f.reg.MustRegister(g)
	return g
}

func (f factory) gaugeVec(name, help string, labels ...string) *prometheus.GaugeVec {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: namespace, Name: name, Help: help}, labels)
	f.reg.MustRegister(g)
	return g
}

func (f factory) histogram(name, help string, buckets []float64, labels ...string) *prometheus.HistogramVec {
	h := prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: namespace, Name: name, Help: help, Buckets: buckets}, labels)
	f.reg.MustRegister(h)
	return h
}

func (f factory) histogramOne(name, help string, buckets []float64) prometheus.Histogram {
	h := prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: namespace, Name: name, Help: help, Buckets: buckets})
	f.reg.MustRegister(h)
	return h
}
