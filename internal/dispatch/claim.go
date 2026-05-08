package dispatch

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ------------------------------------------------------------------
// Dependencies
// ------------------------------------------------------------------

// ClaimDeps holds the external dependencies required by the claim loop.
type ClaimDeps struct {
	// Collection is the MongoDB dispatch_requests collection.
	Collection *mongo.Collection

	// AgentID is the unique identifier of this dispatcher instance.
	// It is written into claimed_by to prevent split-brain double-claims.
	AgentID string

	// ConcurrencyLimit is the maximum number of concurrent tasks this
	// dispatcher may handle (running + claimed).
	ConcurrencyLimit int

	// ActiveCount returns the current number of in-flight tasks (RUNNING
	// spawns + CLAIMED requests) for slot math.  Called once per tick.
	ActiveCount func() int

	// TickInterval controls how often the claim loop polls MongoDB.
	// Defaults to 2 seconds if zero.
	TickInterval time.Duration
}

// ------------------------------------------------------------------
// Public API
// ------------------------------------------------------------------

// RunClaimLoop ticks every 2 seconds, computes free slots, and attempts to
// atomically claim PENDING dispatch_requests via findOneAndUpdate.  Each
// successfully claimed request is pushed to out.  The loop honours
// ctx.Done() for clean shutdown.
func RunClaimLoop(ctx context.Context, deps ClaimDeps, out chan<- *DispatchRequest) error {
	interval := deps.TickInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		free := deps.ConcurrencyLimit - deps.ActiveCount()
		if free <= 0 {
			continue
		}

		for i := 0; i < free; i++ {
			req, err := claimOne(ctx, deps)
			if err != nil {
				if err == mongo.ErrNoDocuments {
					// No more PENDING work — stop filling slots this tick.
					break
				}
				// Log and retry next tick.  We do not return here because
				// transient Mongo errors should not kill the dispatcher.
				continue
			}

			select {
			case out <- req:
				// successfully handed off
			case <-ctx.Done():
				return ctx.Err()
			default:
				// Channel full — drop and retry next tick.  This prevents
				// the claim loop from blocking indefinitely when downstream
				// is back-pressured.
				//
				// Note: the request is already CLAIMED in MongoDB.  The
				// next tick will see it as CLAIMED (not PENDING) so it
				// won't be re-claimed.  Downstream must consume from out
				// or the slot will remain occupied.
			}
		}
	}
}

// ------------------------------------------------------------------
// Internal helpers
// ------------------------------------------------------------------

// claimOne performs an atomic findOneAndUpdate on the dispatch_requests
// collection, transitioning the oldest PENDING request to CLAIMED for this
// agent.  Returns mongo.ErrNoDocuments when no PENDING work exists.
func claimOne(ctx context.Context, deps ClaimDeps) (*DispatchRequest, error) {
	now := time.Now().UTC()

	filter := bson.M{"status": RequestPending}
	update := bson.M{
		"$set": bson.M{
			"status":      RequestClaimed,
			"claimed_by":  deps.AgentID,
			"claimed_at":  now,
			"updated_at":  now,
		},
	}
	opts := options.FindOneAndUpdate().
		SetSort(bson.D{{Key: "queued_at", Value: 1}}).
		SetReturnDocument(options.After)

	var req DispatchRequest
	err := deps.Collection.FindOneAndUpdate(ctx, filter, update, opts).Decode(&req)
	if err != nil {
		return nil, fmt.Errorf("claimOne: %w", err)
	}
	return &req, nil
}
