package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const defaultInstrumentationName = "github.com/renuno-frinardi/IF3110_TugasBesarWBD_HarrowDotCom"

var textMapPropagator = propagation.NewCompositeTextMapPropagator(
	propagation.TraceContext{},
	propagation.Baggage{},
)

func InitTracing(ctx context.Context, serviceName string, out io.Writer) (func(context.Context) error, error) {
	if out == nil {
		out = os.Stdout
	}
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("OTEL_EXPORTER")))
	if mode == "" {
		mode = "stdout"
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
		),
	)
	if err != nil {
		return nil, err
	}

	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	}

	if mode != "none" && mode != "noop" && mode != "off" {
		exp, expErr := stdouttrace.New(
			stdouttrace.WithWriter(NewRedactingWriter(out)),
		)
		if expErr != nil {
			return nil, expErr
		}
		opts = append(opts, sdktrace.WithSyncer(exp))
	}

	tp := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(textMapPropagator)

	return tp.Shutdown, nil
}

func Tracer(component string) trace.Tracer {
	if component == "" {
		component = defaultInstrumentationName
	}
	return otel.Tracer(component)
}

func InjectTraceContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	carrier := propagation.MapCarrier{}
	textMapPropagator.Inject(ctx, carrier)
	if len(carrier) == 0 {
		return ""
	}
	if len(carrier) == 1 {
		if tp := carrier.Get("traceparent"); tp != "" {
			return tp
		}
	}
	raw, err := json.Marshal(carrier)
	if err != nil {
		return carrier.Get("traceparent")
	}
	return string(raw)
}

func ExtractTraceContext(ctx context.Context, encoded string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	trimmed := strings.TrimSpace(encoded)
	if trimmed == "" {
		return ctx
	}
	carrier := propagation.MapCarrier{}
	if strings.HasPrefix(trimmed, "{") {
		var parsed map[string]string
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
			for k, v := range parsed {
				carrier.Set(k, v)
			}
		}
	} else {
		carrier.Set("traceparent", trimmed)
	}
	return textMapPropagator.Extract(ctx, carrier)
}
