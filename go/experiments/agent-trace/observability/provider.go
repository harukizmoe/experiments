package agent_trace_observability

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "go-agent-eval/internal/observability"

type ProviderConfig struct {
	LangfuseBaseURL    string
	OTLPTracesEndpoint string
	PublicKey          string
	SecretKey          string
	ServiceName        string
	ServiceVersion     string
	ExportTimeout      time.Duration
}

type Provider struct {
	tracerProvider *sdktrace.TracerProvider
}

func NewProvider(
	ctx context.Context,
	cfg ProviderConfig,
) (*Provider, error) {
	if strings.TrimSpace(
		cfg.PublicKey,
	) == "" ||
		strings.TrimSpace(
			cfg.SecretKey,
		) == "" {

		return nil, errors.New(
			"Langfuse public and secret keys are required",
		)
	}

	if cfg.ServiceName == "" {
		cfg.ServiceName = "go-agent-eval"
	}

	if cfg.ServiceVersion == "" {
		cfg.ServiceVersion = "phase-3"
	}

	if cfg.ExportTimeout <= 0 {
		cfg.ExportTimeout =
			10 * time.Second
	}

	endpoint, err := tracesEndpoint(
		cfg.LangfuseBaseURL,
		cfg.OTLPTracesEndpoint,
	)
	if err != nil {
		return nil, err
	}

	auth := base64.StdEncoding.EncodeToString(
		[]byte(
			cfg.PublicKey +
				":" +
				cfg.SecretKey,
		),
	)

	exporter, err := otlptracehttp.New(
		ctx,

		otlptracehttp.WithEndpointURL(
			endpoint,
		),

		otlptracehttp.WithHeaders(
			map[string]string{
				"Authorization": "Basic " + auth,

				"x-langfuse-ingestion-version": "4",
			},
		),

		otlptracehttp.WithCompression(
			otlptracehttp.GzipCompression,
		),

		otlptracehttp.WithTimeout(
			cfg.ExportTimeout,
		),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"create Langfuse OTLP exporter: %w",
			err,
		)
	}

	res := resource.NewSchemaless(
		attribute.String(
			"service.name",
			cfg.ServiceName,
		),

		attribute.String(
			"service.version",
			cfg.ServiceVersion,
		),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),

		sdktrace.WithSpanProcessor(
			NewBaggageSpanProcessor(),
		),

		sdktrace.WithBatcher(
			exporter,
		),
	)

	otel.SetTracerProvider(tp)

	otel.SetTextMapPropagator(
		propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	)

	return &Provider{
		tracerProvider: tp,
	}, nil
}

func (p *Provider) Tracer() trace.Tracer {
	return p.tracerProvider.Tracer(
		instrumentationName,
	)
}

func (p *Provider) ForceFlush(
	ctx context.Context,
) error {
	if p == nil ||
		p.tracerProvider == nil {

		return nil
	}

	return p.tracerProvider.ForceFlush(
		ctx,
	)
}

func (p *Provider) Shutdown(
	ctx context.Context,
) error {
	if p == nil ||
		p.tracerProvider == nil {

		return nil
	}

	return p.tracerProvider.Shutdown(
		ctx,
	)
}

func tracesEndpoint(
	baseURL string,
	explicit string,
) (string, error) {
	if endpoint := strings.TrimSpace(
		explicit,
	); endpoint != "" {
		return strings.TrimRight(
			endpoint,
			"/",
		), nil
	}

	base := strings.TrimRight(
		strings.TrimSpace(
			baseURL,
		),
		"/",
	)

	if base == "" {
		return "", errors.New(
			"Langfuse base URL is required",
		)
	}

	if strings.HasSuffix(
		base,
		"/api/public/otel/v1/traces",
	) {
		return base, nil
	}

	if strings.HasSuffix(
		base,
		"/api/public/otel",
	) {
		return base +
			"/v1/traces", nil
	}

	return base +
		"/api/public/otel/v1/traces", nil
}
