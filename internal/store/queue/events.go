// Package queue provides event-related types for the task queue.
package queue

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// EventType enumerates the kinds of task events.
type EventType string

const (
	EventTaskCreated   EventType = "task_created"
	EventTaskClaimed   EventType = "task_claimed"
	EventTaskStarted   EventType = "task_started"
	EventTaskCompleted EventType = "task_completed"
	EventTaskFailed    EventType = "task_failed"
	EventTaskRetried   EventType = "task_retried"
	EventTaskReclaimed EventType = "task_reclaimed"
)

// NewTaskEvent builds a TaskEvent with the current timestamp.
func NewTaskEvent(taskID string, seq int, eventType EventType, actor string, data bson.M) TaskEvent {
	return TaskEvent{
		TaskID:    taskID,
		Sequence:  seq,
		EventType: string(eventType),
		Timestamp: time.Now().UTC(),
		Actor:     actor,
		Data:      data,
	}
}
