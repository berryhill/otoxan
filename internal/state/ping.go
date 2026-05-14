// ping.go — standalone MongoDB connectivity probe.
package state

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Ping verifies that the MongoDB server behind client is reachable.
// It uses a 5-second timeout. On failure it returns a wrapped error
// with context so the CLI can print something useful.
func Ping(client *mongo.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx, nil); err != nil {
		return fmt.Errorf("mongodb ping failed: %w", err)
	}
	return nil
}
