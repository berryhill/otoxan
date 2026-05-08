// Package notifications provides a MongoDB-backed store for notification
// documents with soft-delete semantics.
package notifications

import (
	"time"
)

// NotificationStatus enumerates the possible states of a notification.
type NotificationStatus string

const (
	StatusPending NotificationStatus = "PENDING"
	StatusSent    NotificationStatus = "SENT"
	StatusRead    NotificationStatus = "READ"
	StatusFailed  NotificationStatus = "FAILED"
)

// Channel enumerates the delivery channels.
type Channel string

const (
	ChannelInApp  Channel = "in_app"
	ChannelEmail  Channel = "email"
	ChannelSlack  Channel = "slack"
	ChannelWeb    Channel = "web"
	ChannelPush   Channel = "push"
)

// Notification is the canonical BSON shape for a notification document.
type Notification struct {
	NotificationID string             `bson:"notification_id" json:"notification_id"`
	RecipientID    string             `bson:"recipient_id" json:"recipient_id"`
	Channel        Channel            `bson:"channel" json:"channel"`
	Status         NotificationStatus `bson:"status" json:"status"`
	Title          string             `bson:"title" json:"title"`
	Body           string             `bson:"body" json:"body"`
	Payload        map[string]interface{} `bson:"payload" json:"payload"`
	CreatedAt      time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt      time.Time          `bson:"updated_at" json:"updated_at"`
	SentAt         *time.Time         `bson:"sent_at,omitempty" json:"sent_at,omitempty"`
	ReadAt         *time.Time         `bson:"read_at,omitempty" json:"read_at,omitempty"`

	// Soft-delete fields (managed by softdelete package)
	Deleted   bool       `bson:"deleted,omitempty" json:"deleted,omitempty"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}
