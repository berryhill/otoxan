// Package notifications provides a MongoDB-backed store for notification
// documents with soft-delete semantics.
package notifications

import (
	"context"
	"time"

	"github.com/silas/otoxan/internal/softdelete"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// NewNotificationStore creates a NotificationStore backed by the given MongoDB
// collection. It ensures required indexes on the notifications collection.
func NewNotificationStore(coll *mongo.Collection) *NotificationStore {
	sd := softdelete.NewSoftDelete(coll)
	ns := &NotificationStore{sd: sd}
	_ = ns.ensureIndexes(context.Background())
	return ns
}

// NotificationStore is a MongoDB-backed store for notification documents with
// soft-delete semantics.
type NotificationStore struct {
	sd *softdelete.SoftDeleteCollection
}

// ------------------------------------------------------------------
// Indexes
// ------------------------------------------------------------------

func (s *NotificationStore) ensureIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "notification_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "recipient_id", Value: 1}}},
		{Keys: bson.D{{Key: "channel", Value: 1}}},
		{Keys: bson.D{{Key: "status", Value: 1}}},
		{Keys: bson.D{{Key: "created_at", Value: 1}}},
		{Keys: bson.D{{Key: "sent_at", Value: 1}}, Options: options.Index().SetSparse(true)},
	}
	_, err := s.sd.Database().Collection(s.sd.Name()).Indexes().CreateMany(ctx, indexes)
	return err
}

// ------------------------------------------------------------------
// CRUD
// ------------------------------------------------------------------

// Create inserts a new notification document. The caller must set NotificationID
// and RecipientID. Defaults are applied for optional fields.
func (s *NotificationStore) Create(ctx context.Context, n *Notification) (*mongo.InsertOneResult, error) {
	now := time.Now().UTC()
	if n.CreatedAt.IsZero() {
		n.CreatedAt = now
	}
	if n.UpdatedAt.IsZero() {
		n.UpdatedAt = now
	}
	if n.Status == "" {
		n.Status = StatusPending
	}
	if n.Channel == "" {
		n.Channel = ChannelInApp
	}
	if n.Payload == nil {
		n.Payload = map[string]interface{}{}
	}
	return s.sd.InsertOne(ctx, n)
}

// Get retrieves a single notification by notification_id. Returns
// mongo.ErrNoDocuments if not found (or soft-deleted).
func (s *NotificationStore) Get(ctx context.Context, notificationID string) (*Notification, error) {
	sr := s.sd.FindOne(ctx, bson.M{"notification_id": notificationID})
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var n Notification
	if err := sr.Decode(&n); err != nil {
		return nil, err
	}
	return &n, nil
}

// GetWithDeleted retrieves a notification including soft-deleted ones.
func (s *NotificationStore) GetWithDeleted(ctx context.Context, notificationID string) (*Notification, error) {
	sr := s.sd.FindOne(ctx, bson.M{"notification_id": notificationID}, softdelete.WithIncludeDeleted())
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var n Notification
	if err := sr.Decode(&n); err != nil {
		return nil, err
	}
	return &n, nil
}

// Update patches fields of an existing notification. Sets updated_at automatically.
func (s *NotificationStore) Update(ctx context.Context, notificationID string, updates bson.M) (*mongo.UpdateResult, error) {
	updates["updated_at"] = time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"notification_id": notificationID}, bson.M{"$set": updates})
}

// Delete soft-deletes a notification by notification_id.
func (s *NotificationStore) Delete(ctx context.Context, notificationID string) (*mongo.UpdateResult, error) {
	return s.sd.Delete(ctx, bson.M{"notification_id": notificationID})
}

// Restore un-deletes a soft-deleted notification.
func (s *NotificationStore) Restore(ctx context.Context, notificationID string) (*mongo.UpdateResult, error) {
	return s.sd.Restore(ctx, bson.M{"notification_id": notificationID})
}

// HardDelete permanently removes a notification.
func (s *NotificationStore) HardDelete(ctx context.Context, notificationID string) (*mongo.DeleteResult, error) {
	return s.sd.HardDelete(ctx, bson.M{"notification_id": notificationID})
}

// ------------------------------------------------------------------
// List / Query
// ------------------------------------------------------------------

// ListOptions configures the List query.
type ListOptions struct {
	RecipientID    string
	Channel        []Channel
	Status         []NotificationStatus
	Limit          int
	IncludeDeleted bool
}

// List returns notifications matching the provided filters, sorted by created_at descending.
func (s *NotificationStore) List(ctx context.Context, opts ListOptions) ([]Notification, error) {
	filter := bson.M{}
	if opts.RecipientID != "" {
		filter["recipient_id"] = opts.RecipientID
	}
	if len(opts.Channel) > 0 {
		if len(opts.Channel) == 1 {
			filter["channel"] = opts.Channel[0]
		} else {
			channels := make([]string, len(opts.Channel))
			for i, c := range opts.Channel {
				channels[i] = string(c)
			}
			filter["channel"] = bson.M{"$in": channels}
		}
	}
	if len(opts.Status) > 0 {
		if len(opts.Status) == 1 {
			filter["status"] = opts.Status[0]
		} else {
			statuses := make([]string, len(opts.Status))
			for i, st := range opts.Status {
				statuses[i] = string(st)
			}
			filter["status"] = bson.M{"$in": statuses}
		}
	}

	var sdOpts []softdelete.Option
	if opts.IncludeDeleted {
		sdOpts = append(sdOpts, softdelete.WithIncludeDeleted())
	}

	cur, err := s.sd.Find(ctx, filter, sdOpts...)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var notifications []Notification
	if err := cur.All(ctx, &notifications); err != nil {
		return nil, err
	}

	// Sort by created_at desc, limit in-memory
	sortNotifications(notifications)
	if opts.Limit > 0 && len(notifications) > opts.Limit {
		notifications = notifications[:opts.Limit]
	}
	return notifications, nil
}

// Count returns the number of notifications matching the filter.
func (s *NotificationStore) Count(ctx context.Context, filter bson.M) (int64, error) {
	return s.sd.CountDocuments(ctx, filter)
}

// MarkSent updates a notification to SENT status and records sent_at.
func (s *NotificationStore) MarkSent(ctx context.Context, notificationID string) (*mongo.UpdateResult, error) {
	now := time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"notification_id": notificationID}, bson.M{"$set": bson.M{"status": StatusSent, "sent_at": now, "updated_at": now}})
}

// MarkRead updates a notification to READ status and records read_at.
func (s *NotificationStore) MarkRead(ctx context.Context, notificationID string) (*mongo.UpdateResult, error) {
	now := time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"notification_id": notificationID}, bson.M{"$set": bson.M{"status": StatusRead, "read_at": now, "updated_at": now}})
}

// sortNotifications sorts by created_at descending.
func sortNotifications(notifications []Notification) {
	for i := 0; i < len(notifications); i++ {
		for j := i + 1; j < len(notifications); j++ {
			if notifications[j].CreatedAt.After(notifications[i].CreatedAt) {
				notifications[i], notifications[j] = notifications[j], notifications[i]
			}
		}
	}
}

// ------------------------------------------------------------------
// Collection passthrough
// ------------------------------------------------------------------

// Name returns the underlying collection name.
func (s *NotificationStore) Name() string {
	return s.sd.Name()
}

// Database returns the underlying database.
func (s *NotificationStore) Database() *mongo.Database {
	return s.sd.Database()
}
