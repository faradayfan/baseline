package metrics_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	basemetrics "github.com/faradayfan/baseline/internal/metrics"
	"github.com/faradayfan/baseline/internal/storetest"
)

// collect builds a manual-reader meter provider over pool, registers Baseline's
// instruments, and returns one collected snapshot.
func collect(t *testing.T, pool *pgxpool.Pool) metricdata.ResourceMetrics {
	t.Helper()
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	if _, err := basemetrics.New(mp.Meter("test"), pool); err != nil {
		t.Fatalf("metrics.New: %v", err)
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	return rm
}

// gaugeValue finds a named Int64 gauge and returns its first data point value.
func gaugeValue(t *testing.T, rm metricdata.ResourceMetrics, name string) int64 {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			g, ok := m.Data.(metricdata.Gauge[int64])
			if !ok || len(g.DataPoints) == 0 {
				t.Fatalf("metric %s not an int64 gauge with data", name)
			}
			return g.DataPoints[0].Value
		}
	}
	t.Fatalf("metric %s not found", name)
	return 0
}

func setupNS(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO namespaces (name, kind) VALUES ('org','org') RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestObservableGauges(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	pool := storetest.Shared(t).FreshDB(t)
	ns := setupNS(t, pool)
	ctx := context.Background()

	// Two active facts; one expiring within 24h.
	soon := time.Now().Add(3 * time.Hour)
	if _, err := pool.Exec(ctx, `
		INSERT INTO facts (namespace_id, statement, subject, canonical_key, status, tags, valid_to, created_by, valid_from)
		VALUES
		  ($1,'a','{}'::jsonb,'k1','active','{}',NULL,'s',now()),
		  ($1,'b','{}'::jsonb,'k2','active','{}',$2,'s',now())`, ns, soon); err != nil {
		t.Fatal(err)
	}

	rm := collect(t, pool)
	if got := gaugeValue(t, rm, "facts_active"); got != 2 {
		t.Errorf("facts_active = %d, want 2", got)
	}
	if got := gaugeValue(t, rm, "facts_expiring_24h"); got != 1 {
		t.Errorf("facts_expiring_24h = %d, want 1", got)
	}
}

func TestApprovalLatencyHistogramRegisters(t *testing.T) {
	if testing.Short() {
		t.Skip("integration")
	}
	// Histogram works without a pool (no DB gauges).
	reader := metric.NewManualReader()
	mp := metric.NewMeterProvider(metric.WithReader(reader))
	m, err := basemetrics.New(mp.Meter("test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	m.RecordApprovalLatency(context.Background(), 1.5)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, sm := range rm.ScopeMetrics {
		for _, mm := range sm.Metrics {
			if mm.Name == "approval_latency_seconds" {
				found = true
			}
		}
	}
	if !found {
		t.Error("approval_latency_seconds not recorded")
	}
}
