package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/global"
	controller "go.opentelemetry.io/otel/sdk/metric/controller/basic"
	processor "go.opentelemetry.io/otel/sdk/metric/processor/basic"
	"go.opentelemetry.io/otel/sdk/metric/selector/simple"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/semconv/v1.4.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	ctx := context.Background()

	conn, err := grpc.DialContext(ctx, "opentelemetrycollector.default.svc.cluster.local:4317",
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		log.Fatalf("failed to create gRPC connection to collector: %v", err)
	}

	client := otlpmetricgrpc.NewClient(otlpmetricgrpc.WithGRPCConn(conn))
	exp, err := otlpmetric.New(ctx, client)
	if err != nil {
		log.Fatalf("Failed to create the collector exporter: %v", err)
	}
	defer func() {
		log.Printf("Setting timeout to one second\n")
		ctx, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if err := exp.Shutdown(ctx); err != nil {
			otel.Handle(err)
		}
	}()

	uuidStr := uuid.New().String()
	resources := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String("myService"),
		semconv.ServiceVersionKey.String("1.0.0"),
		semconv.ServiceInstanceIDKey.String(uuidStr),
	)
	pusher := controller.New(
		processor.NewFactory(
			simple.NewWithHistogramDistribution(),
			exp,
		),
		controller.WithExporter(exp),
		controller.WithCollectPeriod(2*time.Second),
		controller.WithResource(resources),
	)
	global.SetMeterProvider(pusher)

	if err := pusher.Start(ctx); err != nil {
		log.Fatalf("could not start metric controoler: %v", err)
	}
	defer func() {
		log.Printf("Setting timeout to 20 seconds\n")
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		// pushes any last exports to the receiver
		if err := pusher.Stop(ctx); err != nil {
			otel.Handle(err)
		}
	}()

	meter := global.Meter("test-meter")

	// Recorder metric example
	counter := metric.Must(meter).
		NewFloat64Counter(
			"an_important_metric",
			metric.WithDescription("Measures the cumulative epicness of the app"),
			metric.WithUnit("ganges"),
		)

	for i := 0; i < 10; i++ {
		log.Printf("Doing really hard work (%d / 10)\n", i+1)
		counter.Add(ctx, 1.0, attribute.String("type", "hits"), attribute.String("mservice", "ganges"))
		counter.Add(ctx, 1.0, attribute.String("type", "misses"), attribute.String("mservice", "ganges"))
	}

	log.Printf("Serving HTTP requests \n")
	r := mux.NewRouter()

	r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		counter.Add(ctx, 1.0, attribute.String("type", "main"), attribute.String("mservice", "ganges"))
		fmt.Fprintf(w, "<h1>This is the homepage. Try /hello and /hello/Sammy\n</h1>")
	})

	r.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		counter.Add(ctx, 1.0, attribute.String("type", "hello endpoint"), attribute.String("mservice", "ganges"))
		fmt.Fprintf(w, "<h1>Hello from Docker!\n</h1>")
	})

	r.HandleFunc("/hello/{name}", func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		title := vars["name"]
		counter.Add(ctx, 1.0, attribute.String("type", "hello{name} endpoint"), attribute.String("mservice", "ganges"))
		fmt.Fprintf(w, "<h1>Hello, %s!\n</h1>", title)
	})

	http.ListenAndServe(":80", r)
}
