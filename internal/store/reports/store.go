// Package reports provides a MongoDB-backed store for report documents with
// soft-delete semantics.
package reports

import (
	"context"
	"errors"
	"time"

	"github.com/silas/otoxan/internal/softdelete"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// MaxBSONDocSize is MongoDB's document size limit (16 MB).
const MaxBSONDocSize = 16 * 1024 * 1024

// NewReportStore creates a ReportStore backed by the given MongoDB collection.
// It ensures required indexes on the reports collection.
func NewReportStore(coll *mongo.Collection) *ReportStore {
	sd := softdelete.NewSoftDelete(coll)
	rs := &ReportStore{sd: sd}
	_ = rs.ensureIndexes(context.Background())
	return rs
}

// ReportStore is a MongoDB-backed store for report documents with soft-delete
// semantics. It mirrors the Python reportstore.ReportStore CRUD surface.
type ReportStore struct {
	sd *softdelete.SoftDeleteCollection
}

// ------------------------------------------------------------------
// Indexes
// ------------------------------------------------------------------

func (s *ReportStore) ensureIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "report_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "status", Value: 1}}},
		{Keys: bson.D{{Key: "updated_at", Value: 1}}},
		{Keys: bson.D{{Key: "archived_at", Value: 1}}, Options: options.Index().SetSparse(true)},
	}
	_, err := s.sd.Database().Collection(s.sd.Name()).Indexes().CreateMany(ctx, indexes)
	return err
}

// ------------------------------------------------------------------
// CRUD
// ------------------------------------------------------------------

// Create inserts a new report document. The caller must set ReportID and Title.
// Defaults are applied for optional fields. Returns an error if the document
// would exceed MongoDB's 16MB document size limit.
func (s *ReportStore) Create(ctx context.Context, report *Report) (*mongo.InsertOneResult, error) {
	now := time.Now().UTC()
	if report.CreatedAt.IsZero() {
		report.CreatedAt = now
	}
	if report.UpdatedAt.IsZero() {
		report.UpdatedAt = now
	}
	if report.Status == "" {
		report.Status = StatusDraft
	}
	if report.Tags == nil {
		report.Tags = []string{}
	}

	// Guard against documents exceeding MongoDB's 16MB limit
	size, err := bson.Marshal(report)
	if err != nil {
		return nil, err
	}
	if len(size) > MaxBSONDocSize {
		return nil, errors.New("report document exceeds 16MB BSON limit")
	}

	return s.sd.InsertOne(ctx, report)
}

// Get retrieves a single report by report_id. Returns mongo.ErrNoDocuments if
// not found (or soft-deleted).
func (s *ReportStore) Get(ctx context.Context, reportID string) (*Report, error) {
	sr := s.sd.FindOne(ctx, bson.M{"report_id": reportID})
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var r Report
	if err := sr.Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// GetWithDeleted retrieves a report including soft-deleted ones.
func (s *ReportStore) GetWithDeleted(ctx context.Context, reportID string) (*Report, error) {
	sr := s.sd.FindOne(ctx, bson.M{"report_id": reportID}, softdelete.WithIncludeDeleted())
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var r Report
	if err := sr.Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// Update patches fields of an existing report. Sets updated_at automatically.
func (s *ReportStore) Update(ctx context.Context, reportID string, updates bson.M) (*mongo.UpdateResult, error) {
	updates["updated_at"] = time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"report_id": reportID}, bson.M{"$set": updates})
}

// Delete soft-deletes a report by report_id.
func (s *ReportStore) Delete(ctx context.Context, reportID string) (*mongo.UpdateResult, error) {
	return s.sd.Delete(ctx, bson.M{"report_id": reportID})
}

// Restore un-deletes a soft-deleted report.
func (s *ReportStore) Restore(ctx context.Context, reportID string) (*mongo.UpdateResult, error) {
	return s.sd.Restore(ctx, bson.M{"report_id": reportID})
}

// HardDelete permanently removes a report.
func (s *ReportStore) HardDelete(ctx context.Context, reportID string) (*mongo.DeleteResult, error) {
	return s.sd.HardDelete(ctx, bson.M{"report_id": reportID})
}

// ------------------------------------------------------------------
// List / Query
// ------------------------------------------------------------------

// ListOptions configures the List query.
type ListOptions struct {
	Status          []ReportStatus
	Tag             string
	Limit           int
	IncludeDeleted  bool
	IncludeArchived bool
}

// List returns reports matching the provided filters, sorted by updated_at descending.
func (s *ReportStore) List(ctx context.Context, opts ListOptions) ([]Report, error) {
	filter := bson.M{}
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
	if opts.Tag != "" {
		filter["tags"] = opts.Tag
	}
	if !opts.IncludeArchived {
		filter["archived_at"] = bson.M{"$eq": nil}
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

	var reports []Report
	if err := cur.All(ctx, &reports); err != nil {
		return nil, err
	}

	// Sort by updated_at desc, limit in-memory
	sortReports(reports)
	if opts.Limit > 0 && len(reports) > opts.Limit {
		reports = reports[:opts.Limit]
	}
	return reports, nil
}

// Count returns the number of reports matching the filter.
func (s *ReportStore) Count(ctx context.Context, filter bson.M) (int64, error) {
	return s.sd.CountDocuments(ctx, filter)
}

// Archive sets archived_at on a report and changes status to ARCHIVED.
func (s *ReportStore) Archive(ctx context.Context, reportID string) (*mongo.UpdateResult, error) {
	now := time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"report_id": reportID}, bson.M{"$set": bson.M{
		"archived_at": now,
		"updated_at":  now,
		"status":      StatusArchived,
	}})
}

// Unarchive clears archived_at on a report and changes status to DRAFT.
func (s *ReportStore) Unarchive(ctx context.Context, reportID string) (*mongo.UpdateResult, error) {
	now := time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"report_id": reportID}, bson.M{"$set": bson.M{
		"archived_at": nil,
		"updated_at":  now,
		"status":      StatusDraft,
	}})
}

// Publish changes a report status to PUBLISHED.
func (s *ReportStore) Publish(ctx context.Context, reportID string) (*mongo.UpdateResult, error) {
	now := time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"report_id": reportID}, bson.M{"$set": bson.M{
		"status":     StatusPublished,
		"updated_at": now,
	}})
}

// Unpublish changes a report status back to DRAFT.
func (s *ReportStore) Unpublish(ctx context.Context, reportID string) (*mongo.UpdateResult, error) {
	now := time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"report_id": reportID}, bson.M{"$set": bson.M{
		"status":     StatusDraft,
		"updated_at": now,
	}})
}

// LinkPlan sets the linked_plan_id on a report.
func (s *ReportStore) LinkPlan(ctx context.Context, reportID string, planID string) (*mongo.UpdateResult, error) {
	now := time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"report_id": reportID}, bson.M{"$set": bson.M{
		"linked_plan_id": planID,
		"updated_at":     now,
	}})
}

// UnlinkPlan clears the linked_plan_id on a report.
func (s *ReportStore) UnlinkPlan(ctx context.Context, reportID string) (*mongo.UpdateResult, error) {
	now := time.Now().UTC()
	return s.sd.UpdateOne(ctx, bson.M{"report_id": reportID}, bson.M{"$set": bson.M{
		"linked_plan_id": nil,
		"updated_at":     now,
	}})
}

// sortReports sorts by updated_at descending.
func sortReports(reports []Report) {
	for i := 0; i < len(reports); i++ {
		for j := i + 1; j < len(reports); j++ {
			if reports[j].UpdatedAt.After(reports[i].UpdatedAt) {
				reports[i], reports[j] = reports[j], reports[i]
			}
		}
	}
}

// ------------------------------------------------------------------
// Collection passthrough
// ------------------------------------------------------------------

// Name returns the underlying collection name.
func (s *ReportStore) Name() string {
	return s.sd.Name()
}

// Database returns the underlying database.
func (s *ReportStore) Database() *mongo.Database {
	return s.sd.Database()
}
