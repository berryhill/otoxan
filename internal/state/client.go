// Package state provides a singleton MongoDB client wrapper for otoxan.
// All durable state — per-agent and global — is read and written through
// this layer. No JSON files, SQLite, or per-agent state files remain
// under ~/.otoxan/.
package state

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ------------------------------------------------------------------
// Singleton client
// ------------------------------------------------------------------

var (
	clientOnce sync.Once
	clientInst *mongo.Client
	clientErr  error
)

// OpenClient returns a shared *mongo.Client for the given MongoDB URI.
// The first call connects and caches the client; subsequent calls return
// the same instance. The caller is responsible for calling Disconnect
// (usually once at process shutdown).
func OpenClient(uri string) (*mongo.Client, error) {
	clientOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		clientInst, clientErr = mongo.Connect(options.Client().ApplyURI(uri))
		if clientErr != nil {
			return
		}
		// Verify the connection is alive.
		if err := clientInst.Ping(ctx, nil); err != nil {
			_ = clientInst.Disconnect(ctx)
			clientInst = nil
			clientErr = fmt.Errorf("ping mongodb: %w", err)
		}
	})
	return clientInst, clientErr
}

// ResetClient clears the singleton instance. Used only in tests.
func ResetClient() {
	clientOnce = sync.Once{}
	clientInst = nil
	clientErr = nil
}
