// Package queue provides the data model for the task queue.
package queue

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// TaskEvent is a single event emitted for a task.
type TaskEvent struct {
	TaskID    string    `bson:"task_id" json:"task_id"`
	Sequence  int       `bson:"sequence" json:"sequence"`
	EventType string    `bson:"event_type" json:"event_type"`
	Timestamp time.Time `bson:"timestamp" json:"timestamp"`
	Actor     string    `bson:"actor" json:"actor"`
	Data      bson.M    `bson:"data" json:"data"`

	// Soft-delete fields (managed by softdelete package)
	Deleted   bool       `bson:"deleted,omitempty" json:"deleted,omitempty"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}

// StatusCount holds the count for a single status.
type StatusCount struct {
	Status string `bson:"_id" json:"status"`
	Count  int64  `bson:"count" json:"count"`
}
