package metrics

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

type poolCollector struct {
	pool          *pgxpool.Pool
	conns         *prometheus.Desc
	maxConns      *prometheus.Desc
	acquires      *prometheus.Desc
	acquireTime   *prometheus.Desc
	emptyAcquires *prometheus.Desc
	canceled      *prometheus.Desc
	newConns      *prometheus.Desc
	destroyed     *prometheus.Desc
}

func (m *Metrics) RegisterPool(pool *pgxpool.Pool) error {
	d := func(name, help string, labels ...string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(namespace, "db_pool", name), help, labels, nil)
	}
	return m.registry.Register(&poolCollector{
		pool:          pool,
		conns:         d("connections", "Pool connections by state.", "state"),
		maxConns:      d("max_connections", "Maximum pool size."),
		acquires:      d("acquires_total", "Successful connection acquires."),
		acquireTime:   d("acquire_seconds_total", "Total time spent acquiring connections."),
		emptyAcquires: d("empty_acquires_total", "Acquires that had to wait for a connection."),
		canceled:      d("canceled_acquires_total", "Acquires canceled by context."),
		newConns:      d("new_connections_total", "Connections opened."),
		destroyed:     d("destroyed_connections_total", "Connections closed by reason.", "reason"),
	})
}

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.conns, c.maxConns, c.acquires, c.acquireTime, c.emptyAcquires, c.canceled, c.newConns, c.destroyed} {
		ch <- d
	}
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.pool.Stat()
	gauge := func(d *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
	}
	counter := func(d *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v, labels...)
	}
	gauge(c.conns, float64(s.AcquiredConns()), "acquired")
	gauge(c.conns, float64(s.IdleConns()), "idle")
	gauge(c.conns, float64(s.ConstructingConns()), "constructing")
	gauge(c.maxConns, float64(s.MaxConns()))
	counter(c.acquires, float64(s.AcquireCount()))
	counter(c.acquireTime, s.AcquireDuration().Seconds())
	counter(c.emptyAcquires, float64(s.EmptyAcquireCount()))
	counter(c.canceled, float64(s.CanceledAcquireCount()))
	counter(c.newConns, float64(s.NewConnsCount()))
	counter(c.destroyed, float64(s.MaxLifetimeDestroyCount()), "lifetime")
	counter(c.destroyed, float64(s.MaxIdleDestroyCount()), "idle")
}
