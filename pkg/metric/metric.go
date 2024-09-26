package metrics

import (
	"context"
	"time"

	"github.com/labring/cri-shim/pkg/types"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.9.0"
)

const MeterName = "cri-shim/vm"

// SetupOTelSDK bootstraps the OpenTelemetry pipeline.
// If it does not return an error, make sure to call shutdown for proper cleanup.
func SetupOTelSDK(config types.MetricsConfig) (shutdown func(context.Context) error, err error) {
	// Set up propagator.
	prop := newPropagator()
	otel.SetTextMapPropagator(prop)

	// Set up meter provider.
	meterProvider, err := newMeterProvider(config)
	if err != nil {
		return
	}
	otel.SetMeterProvider(meterProvider)

	return meterProvider.Shutdown, nil
}

func newMeterProvider(config types.MetricsConfig) (*metric.MeterProvider, error) {
	options := []otlpmetrichttp.Option{
		otlpmetrichttp.WithEndpoint(config.Endpoint),
		otlpmetrichttp.WithURLPath(config.IngestPath),
	}
	if !config.IsSecure {
		options = append(options, otlpmetrichttp.WithInsecure())
	}
	metricExporter, err := otlpmetrichttp.New(context.Background(), options...)
	if err != nil {
		return nil, err
	}

	res, err := newResource(config)
	meterProvider := metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(metric.NewPeriodicReader(metricExporter,
			// Default is 1m. Set to 3s for demonstrative purposes.
			metric.WithInterval(time.Duration(config.PushInterval)*time.Second))),
	)
	meterProvider.Meter(MeterName)
	return meterProvider, nil
}

func newResource(config types.MetricsConfig) (*resource.Resource, error) {
	return resource.Merge(resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL,
			attribute.String("job", config.JobName),
			attribute.String("instance", config.Instance),
		))
}

func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}
