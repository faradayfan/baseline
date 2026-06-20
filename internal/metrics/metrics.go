// Package metrics defines Baseline's OTEL instruments (§13). The named metrics
// are:
//
//   - promotion_queue_depth{namespace} — gauge, in-review promotions per namespace
//   - approval_latency_seconds          — histogram, propose→active duration
//   - facts_active                      — gauge, count of active facts
//   - facts_expiring_24h                — gauge, active facts with valid_to ≤ now+24h
//   - conflicts_open                    — gauge, pending promotions with a conflict
//
// The gauges are OTEL observable instruments backed by COUNT queries, refreshed
// each collection cycle; the histogram is recorded by the promotion workflow.
package metrics

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Metrics holds the registered instruments. Construct once at startup with New.
type Metrics struct {
	approvalLatency metric.Float64Histogram
}

// New registers all instruments against meter and wires the DB-backed observable
// gauges to query pool on each collection. A nil pool disables the gauges (useful
// in tests that only exercise the histogram).
func New(meter metric.Meter, pool *pgxpool.Pool) (*Metrics, error) {
	m := &Metrics{}

	var err error
	m.approvalLatency, err = meter.Float64Histogram(
		"approval_latency_seconds",
		metric.WithDescription("Seconds from fact proposal to activation"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, fmt.Errorf("metrics: approval_latency_seconds: %w", err)
	}

	if pool == nil {
		return m, nil
	}

	factsActive, err := meter.Int64ObservableGauge("facts_active",
		metric.WithDescription("Number of active facts"))
	if err != nil {
		return nil, err
	}
	factsExpiring, err := meter.Int64ObservableGauge("facts_expiring_24h",
		metric.WithDescription("Active facts whose valid_to is within 24h"))
	if err != nil {
		return nil, err
	}
	conflictsOpen, err := meter.Int64ObservableGauge("conflicts_open",
		metric.WithDescription("Pending promotions that conflict with an active fact"))
	if err != nil {
		return nil, err
	}
	queueDepth, err := meter.Int64ObservableGauge("promotion_queue_depth",
		metric.WithDescription("In-review promotions, per namespace"))
	if err != nil {
		return nil, err
	}

	_, err = meter.RegisterCallback(
		func(ctx context.Context, o metric.Observer) error {
			if n, err := scalar(ctx, pool, `SELECT count(*) FROM facts WHERE status='active'`); err == nil {
				o.ObserveInt64(factsActive, n)
			}
			if n, err := scalar(ctx, pool,
				`SELECT count(*) FROM facts WHERE status='active' AND valid_to IS NOT NULL AND valid_to <= now() + interval '24 hours'`); err == nil {
				o.ObserveInt64(factsExpiring, n)
			}
			if n, err := scalar(ctx, pool,
				`SELECT count(*) FROM promotion_requests WHERE state IN ('pending','in_review') AND conflict_with IS NOT NULL`); err == nil {
				o.ObserveInt64(conflictsOpen, n)
			}
			if err := observeQueueDepth(ctx, pool, o, queueDepth); err != nil {
				return err
			}
			return nil
		},
		factsActive, factsExpiring, conflictsOpen, queueDepth,
	)
	if err != nil {
		return nil, fmt.Errorf("metrics: register callback: %w", err)
	}
	return m, nil
}

// RecordApprovalLatency records the propose→active duration in seconds.
func (m *Metrics) RecordApprovalLatency(ctx context.Context, seconds float64) {
	if m == nil || m.approvalLatency == nil {
		return
	}
	m.approvalLatency.Record(ctx, seconds)
}

// observeQueueDepth reports in-review promotion counts labeled by namespace.
func observeQueueDepth(ctx context.Context, pool *pgxpool.Pool, o metric.Observer, g metric.Int64ObservableGauge) error {
	rows, err := pool.Query(ctx, `
		SELECT target_namespace_id::text, count(*) FROM promotion_requests
		WHERE state IN ('pending','in_review') GROUP BY target_namespace_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ns string
		var n int64
		if err := rows.Scan(&ns, &n); err != nil {
			return err
		}
		o.ObserveInt64(g, n, metric.WithAttributes(attribute.String("namespace", ns)))
	}
	return rows.Err()
}

func scalar(ctx context.Context, pool *pgxpool.Pool, sql string) (int64, error) {
	var n int64
	err := pool.QueryRow(ctx, sql).Scan(&n)
	return n, err
}
