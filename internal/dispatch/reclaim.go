package dispatch

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ------------------------------------------------------------------
// Dependencies
// ------------------------------------------------------------------

// ReclaimDeps holds the external dependencies required by the reclaim loop.
type ReclaimDeps struct {
	// Collection is the MongoDB dispatch_requests collection.
	Collection *mongo.Collection

	// Logger is a minimal logger interface.
	// If nil, logging is silently discarded.
	Logger interface {
		Printf(format string, v ...interface{})
	}

	// StaleThreshold is the duration after which a CLAIMED request is
	// considered stale.  Defaults to 10 minutes if zero.
	StaleThreshold time.Duration
}

// ------------------------------------------------------------------
// Public API
// ------------------------------------------------------------------

// reclaimStale finds all dispatch_requests that are CLAIMED but whose
// claimed_at timestamp is older than the stale threshold, and resets
// them back to PENDING (clearing claimed_by and claimed_at).  It returns
// the number of documents updated and any error encountered.
//
// This is best-effort: concurrent claim loops may race on the same row,
// but the atomic find-and-update in claimOne will win for fresh rows.
func reclaimStale(ctx context.Context, deps ReclaimDeps) (int, error) {
	threshold := deps.StaleThreshold
	if threshold == 0 {
		threshold = 10 * time.Minute
	}

	cutoff := time.Now().UTC().Add(-threshold)

	filter := bson.M{
		"status":     RequestClaimed,
		"claimed_at": bson.M{"$lt": cutoff},
	}
	update := bson.M{
		"$set": bson.M{
			"status":     RequestPending,
			"claimed_by": nil,
			"updated_at": time.Now().UTC(),
		},
		"$unset": bson.M{
			"claimed_at": "",
		},
	}

	res, err := deps.Collection.UpdateMany(ctx, filter, update)
	if err != nil {
		return 0, fmt.Errorf("reclaimStale: UpdateMany: %w", err)
	}

	if res.ModifiedCount > 0 {
		logf(deps.Logger, "reclaimStale: reset %d stale CLAIMED request(s) to PENDING", res.ModifiedCount)
	}

	return int(res.ModifiedCount), nil
}
