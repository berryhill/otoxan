package main

import (
	"context"
	"fmt"
	"os"

	"github.com/silas/otoxan/internal/mcp"
	"github.com/silas/otoxan/pkg/identity"
	"github.com/silas/otoxan/pkg/stores/identitystore"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const version = "0.1.0-dev"

func main() {
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

	identityStore, err := identitystore.NewStore(client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "new store: %v\n", err)
		os.Exit(1)
	}

	resolver := identity.NewResolver(identityStore)

	srv := mcp.New("otoxan-mcp-identity", version)
	registerTools(srv, resolver)

	if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		os.Exit(1)
	}
}
