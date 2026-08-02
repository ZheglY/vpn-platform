package postgres

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

type queryTraceKey struct{}

type queryTrace struct {
	operation string
	startedAt time.Time
}

type queryMetrics struct {
	duration *prometheus.HistogramVec
}

func newQueryMetrics(registerer prometheus.Registerer, service string) *queryMetrics {
	registerer = prometheus.WrapRegistererWith(prometheus.Labels{"service": service}, registerer)
	metrics := &queryMetrics{
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "vpn_platform",
			Subsystem: "postgres",
			Name:      "query_duration_seconds",
			Help:      "PostgreSQL query duration by bounded SQL operation class and outcome.",
			Buckets:   []float64{0.001, 0.003, 0.01, 0.03, 0.1, 0.3, 1, 3},
		}, []string{"operation", "outcome"}),
	}
	registerer.MustRegister(metrics.duration)
	return metrics
}

func (m *queryMetrics) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, queryTraceKey{}, queryTrace{
		operation: queryOperation(data.SQL),
		startedAt: time.Now(),
	})
}

func (m *queryMetrics) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	trace, ok := ctx.Value(queryTraceKey{}).(queryTrace)
	if !ok {
		return
	}
	outcome := "success"
	if data.Err != nil {
		outcome = "error"
	}
	m.duration.WithLabelValues(trace.operation, outcome).Observe(time.Since(trace.startedAt).Seconds())
}

func queryOperation(sql string) string {
	fields := strings.Fields(sql)
	if len(fields) == 0 {
		return "other"
	}
	switch strings.ToUpper(fields[0]) {
	case "SELECT":
		return "select"
	case "INSERT":
		return "insert"
	case "UPDATE":
		return "update"
	case "DELETE":
		return "delete"
	case "WITH":
		return "with"
	case "BEGIN", "COMMIT", "ROLLBACK", "SAVEPOINT", "RELEASE":
		return "transaction"
	default:
		return "other"
	}
}

type poolCollector struct {
	pool             *pgxpool.Pool
	connections      *prometheus.Desc
	acquires         *prometheus.Desc
	acquireDuration  *prometheus.Desc
	emptyAcquires    *prometheus.Desc
	emptyWait        *prometheus.Desc
	canceledAcquires *prometheus.Desc
	newConnections   *prometheus.Desc
	closed           *prometheus.Desc
}

func newPoolCollector(pool *pgxpool.Pool) *poolCollector {
	return &poolCollector{
		pool: pool,
		connections: prometheus.NewDesc(
			"vpn_platform_postgres_pool_connections",
			"Current PostgreSQL pool connections by bounded state.",
			[]string{"state"}, nil,
		),
		acquires: prometheus.NewDesc(
			"vpn_platform_postgres_pool_acquires_total",
			"Total successful PostgreSQL pool acquisitions.",
			nil, nil,
		),
		acquireDuration: prometheus.NewDesc(
			"vpn_platform_postgres_pool_acquire_duration_seconds_total",
			"Total time spent acquiring PostgreSQL pool connections.",
			nil, nil,
		),
		emptyAcquires: prometheus.NewDesc(
			"vpn_platform_postgres_pool_empty_acquires_total",
			"Total successful PostgreSQL acquisitions that waited because the pool was empty.",
			nil, nil,
		),
		emptyWait: prometheus.NewDesc(
			"vpn_platform_postgres_pool_empty_acquire_wait_seconds_total",
			"Total wait time for successful PostgreSQL acquisitions while the pool was empty.",
			nil, nil,
		),
		canceledAcquires: prometheus.NewDesc(
			"vpn_platform_postgres_pool_canceled_acquires_total",
			"Total PostgreSQL pool acquisitions canceled by context.",
			nil, nil,
		),
		newConnections: prometheus.NewDesc(
			"vpn_platform_postgres_pool_new_connections_total",
			"Total PostgreSQL connections opened by the pool.",
			nil, nil,
		),
		closed: prometheus.NewDesc(
			"vpn_platform_postgres_pool_closed_connections_total",
			"Total PostgreSQL pool connections closed by bounded reason.",
			[]string{"reason"}, nil,
		),
	}
}

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.connections
	ch <- c.acquires
	ch <- c.acquireDuration
	ch <- c.emptyAcquires
	ch <- c.emptyWait
	ch <- c.canceledAcquires
	ch <- c.newConnections
	ch <- c.closed
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	stat := c.pool.Stat()
	for state, value := range map[string]float64{
		"acquired":     float64(stat.AcquiredConns()),
		"constructing": float64(stat.ConstructingConns()),
		"idle":         float64(stat.IdleConns()),
		"max":          float64(stat.MaxConns()),
		"total":        float64(stat.TotalConns()),
	} {
		ch <- prometheus.MustNewConstMetric(c.connections, prometheus.GaugeValue, value, state)
	}
	ch <- prometheus.MustNewConstMetric(c.acquires, prometheus.CounterValue, float64(stat.AcquireCount()))
	ch <- prometheus.MustNewConstMetric(c.acquireDuration, prometheus.CounterValue, stat.AcquireDuration().Seconds())
	ch <- prometheus.MustNewConstMetric(c.emptyAcquires, prometheus.CounterValue, float64(stat.EmptyAcquireCount()))
	ch <- prometheus.MustNewConstMetric(c.emptyWait, prometheus.CounterValue, stat.EmptyAcquireWaitTime().Seconds())
	ch <- prometheus.MustNewConstMetric(c.canceledAcquires, prometheus.CounterValue, float64(stat.CanceledAcquireCount()))
	ch <- prometheus.MustNewConstMetric(c.newConnections, prometheus.CounterValue, float64(stat.NewConnsCount()))
	ch <- prometheus.MustNewConstMetric(c.closed, prometheus.CounterValue, float64(stat.MaxIdleDestroyCount()), "idle")
	ch <- prometheus.MustNewConstMetric(c.closed, prometheus.CounterValue, float64(stat.MaxLifetimeDestroyCount()), "lifetime")
}
