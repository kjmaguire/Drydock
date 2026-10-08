package main

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/semconv/v1.41.0/genaiconv"
)

var _ = genaiconv.OperationNameChat

func main() {
	exp, _ := otlptracehttp.New(context.Background(), otlptracehttp.WithEndpointURL("http://127.0.0.1:4318"), otlptracehttp.WithInsecure())
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp))
	otel.SetTracerProvider(tp)
	_, sp := otel.Tracer("x").Start(context.Background(), "chat")
	sp.SetAttributes(semconv.GenAIOperationNameChat, attribute.Int("x", 1))
	sp.End()
	tp.Shutdown(context.Background())
}
