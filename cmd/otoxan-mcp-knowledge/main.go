package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/silas/otoxan/internal/mcp"
	"github.com/silas/otoxan/internal/store/memory"
	"github.com/silas/otoxan/internal/store/plans"
	"github.com/silas/otoxan/internal/store/reports"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const version = "0.1.0-dev"

func main() {
	healthCheck := flag.Bool("health-check", false, "Run health check (connect to Mongo and exit)")
	flag.Parse()

	ctx := context.Background()

	uri := os.Getenv("MONGO_URI")
	if uri == "" {
		uri = "mongodb://localhost:27017"
	}
	dbName := os.Getenv("MONGO_DB")
	if dbName == "" {
		dbName = "otoxan"
	}

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = client.Disconnect(ctx) }()

	// Health check mode: ping Mongo and exit
	if *healthCheck {
		if err := client.Ping(ctx, nil); err != nil {
			fmt.Fprintf(os.Stderr, "health-check ping: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	memStore := memory.NewMemoryStore(client.Database(dbName).Collection("memories"), nil)
	planStore := plans.NewPlanStore(client.Database(dbName).Collection("plans"))
	reportStore := reports.NewReportStore(client.Database(dbName).Collection("reports"))

	srv := mcp.New("otoxan-mcp-knowledge", version)
	registerTools(srv, memStore, planStore, reportStore)

	if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		os.Exit(1)
	}
}
