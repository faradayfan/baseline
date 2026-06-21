package platform

import (
	"context"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/trace"
)

// OTel holds the configured providers and a tracer/meter for the app. Shutdown
// flushes exporters. With no exporter configured it uses in-process providers so
// instrumentation is always safe to call (spans/metrics are simply not exported).
type OTel struct {
	Tracer    trace.Tracer
	meterDone func(context.Context) error
}

// SetupOTel installs a MeterProvider and returns handles. For v1 it registers an
// in-process metric reader (no exporter wiring yet); production points OTEL_*
// env at the existing collector. Span export is likewise a no-op until an
// exporter is configured — but the span middleware still creates spans so the
// natural boundaries (§13) exist for when export is turned on.
func SetupOTel(serviceName string) *OTel {
	mp := sdkmetric.NewMeterProvider()
	otel.SetMeterProvider(mp)

	return &OTel{
		Tracer:    otel.Tracer(serviceName),
		meterDone: mp.Shutdown,
	}
}

// Shutdown flushes and stops providers.
func (o *OTel) Shutdown(ctx context.Context) error {
	if o == nil || o.meterDone == nil {
		return nil
	}
	return o.meterDone(ctx)
}

// SpanMiddleware wraps each request in a span (a natural span boundary, §13).
// Span export is governed by the configured tracer provider; this always creates
// the span so boundaries are present regardless of exporter wiring.
func (o *OTel) SpanMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, span := o.Tracer.Start(r.Context(), r.Method+" "+r.URL.Path)
		start := time.Now()
		defer func() {
			span.SetAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.target", r.URL.Path),
				attribute.Float64("duration_ms", float64(time.Since(start).Milliseconds())),
			)
			span.End()
		}()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
