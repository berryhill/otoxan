// Package reports provides a MongoDB-backed store for report documents with
// soft-delete semantics.
package reports

import (
	"time"
)

// ReportStatus enumerates the possible states of a report.
type ReportStatus string

const (
	StatusDraft     ReportStatus = "DRAFT"
	StatusPublished ReportStatus = "PUBLISHED"
	StatusArchived  ReportStatus = "ARCHIVED"
)

// Report is the canonical BSON shape for a report document.
type Report struct {
	ReportID        string    `bson:"report_id" json:"report_id"`
	Title           string    `bson:"title" json:"title"`
	Status          ReportStatus `bson:"status" json:"status"`
	Owner           string    `bson:"owner" json:"owner"`
	CreatedAt       time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt       time.Time `bson:"updated_at" json:"updated_at"`
	Content         string    `bson:"content" json:"content"`
	Tags            []string  `bson:"tags" json:"tags"`
	LinkedPlanID    *string   `bson:"linked_plan_id,omitempty" json:"linked_plan_id,omitempty"`
	CreatedSession  string    `bson:"created_session" json:"created_session"`
	UpdatedSessions []string  `bson:"updated_sessions" json:"updated_sessions"`
	ArchivedAt      *time.Time `bson:"archived_at,omitempty" json:"archived_at,omitempty"`

	// Soft-delete fields (managed by softdelete package)
	Deleted   bool       `bson:"deleted,omitempty" json:"deleted,omitempty"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}
