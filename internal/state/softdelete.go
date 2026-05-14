// softdelete.go — MongoDB soft-delete helper.
//
// Every document that supports soft delete carries a deleted_at field.
// When nil / zero the document is active. When set, it is logically deleted
// but still present in the collection. All list/find queries should use
// DefaultFilter; admin / audit surfaces may use IncludeDeleted.
package state

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ------------------------------------------------------------------
// Document model
// ------------------------------------------------------------------

// SoftDeleteDoc is the base shape for any collection that supports soft
// delete. Embed it anonymously in your struct:
//
//	type MyDoc struct {
//		ID        bson.ObjectID `bson:"_id,omitempty"`
//		Name      string        `bson:"name"`
//		SoftDeleteDoc `bson:",inline"`
//	}
type SoftDeleteDoc struct {
	DeletedAt *time.Time `bson:"deleted_at,omitempty"`
}

// IsDeleted reports whether the document has been soft-deleted.
func (d SoftDeleteDoc) IsDeleted() bool {
	return d.DeletedAt != nil && !d.DeletedAt.IsZero()
}

// ------------------------------------------------------------------
// Query helpers
// ------------------------------------------------------------------

// DefaultFilter returns a bson.D that excludes soft-deleted documents.
// Pass the result to Find, CountDocuments, etc.
func DefaultFilter() bson.D {
	return bson.D{{Key: "deleted_at", Value: bson.M{"$exists": false}}}
}

// IncludeDeleted returns a bson.D that matches *all* documents, including
// soft-deleted ones. Use this for admin / audit queries.
func IncludeDeleted() bson.D {
	return bson.D{}
}

// ------------------------------------------------------------------
// Operations on a single collection
// ------------------------------------------------------------------

// SoftDelete marks the document with id as deleted by setting deleted_at.
// It returns mongo.ErrNoDocuments if the document does not exist.
func SoftDelete(ctx context.Context, coll *mongo.Collection, id any) error {
	filter := bson.D{{Key: "_id", Value: id}}
	update := bson.D{
		{Key: "$set", Value: bson.D{{Key: "deleted_at", Value: time.Now().UTC()}}},
	}
	opts := options.UpdateOne().SetUpsert(false)
	res, err := coll.UpdateOne(ctx, filter, update, opts)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return nil
}

// Restore clears the deleted_at field on the document with id.
// It returns mongo.ErrNoDocuments if the document does not exist.
func Restore(ctx context.Context, coll *mongo.Collection, id any) error {
	filter := bson.D{{Key: "_id", Value: id}}
	update := bson.D{
		{Key: "$unset", Value: bson.D{{Key: "deleted_at", Value: ""}}},
	}
	opts := options.UpdateOne().SetUpsert(false)
	res, err := coll.UpdateOne(ctx, filter, update, opts)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return nil
}

// HardDelete permanently removes the document with id. Use sparingly.
func HardDelete(ctx context.Context, coll *mongo.Collection, id any) error {
	filter := bson.D{{Key: "_id", Value: id}}
	_, err := coll.DeleteOne(ctx, filter)
	return err
}
