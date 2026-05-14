// Package companion provides the Go companion daemon store for browser captures
// awaiting dispatch. Captures are chunked during upload and expire automatically
// via a TTL index.
package companion

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	collectionName = "companion_captures"
	defaultTTL     = 24 * time.Hour
)

// CaptureChunk is a single chunk of a multi-part capture upload.
type CaptureChunk struct {
	Seq  int    `bson:"seq" json:"seq"`
	Data []byte `bson:"data" json:"data"`
}

// CaptureRecord is the canonical BSON shape for a completed capture in MongoDB.
type CaptureRecord struct {
	CaptureID  string         `bson:"capture_id" json:"capture_id"`
	Message    string         `bson:"message" json:"message"`
	Chunks     []CaptureChunk `bson:"chunks" json:"chunks"`
	CreatedAt  time.Time      `bson:"created_at" json:"created_at"`
	ExpiresAt  time.Time      `bson:"expires_at" json:"expires_at"`
	FinishedAt *time.Time     `bson:"finished_at,omitempty" json:"finished_at,omitempty"`
}

// PendingUpload is an in-progress chunked upload tracked in memory.
type PendingUpload struct {
	UploadID   string
	Chunks     map[int]CaptureChunk
	CreatedAt  time.Time
	ExpiresAt  time.Time
	mu         sync.Mutex
}

// CapturesStore wraps the companion_captures MongoDB collection.
type CapturesStore struct {
	coll          *mongo.Collection
	pending       map[string]*PendingUpload
	pendingMu     sync.RWMutex
	ensureOnce    sync.Once
}

// NewCapturesStore creates a CapturesStore backed by the given MongoDB database.
// It ensures the TTL index on expires_at at first use.
func NewCapturesStore(db *mongo.Database) *CapturesStore {
	return &CapturesStore{
		coll:    db.Collection(collectionName),
		pending: make(map[string]*PendingUpload),
	}
}

// ------------------------------------------------------------------
// Indexes
// ------------------------------------------------------------------

func (s *CapturesStore) ensureIndexes(ctx context.Context) error {
	var err error
	s.ensureOnce.Do(func() {
		indexes := []mongo.IndexModel{
			{
				Keys:    bson.D{{Key: "capture_id", Value: 1}},
				Options: options.Index().SetUnique(true),
			},
			{
				Keys:    bson.D{{Key: "expires_at", Value: 1}},
				Options: options.Index().SetExpireAfterSeconds(0),
			},
		}
		_, err = s.coll.Indexes().CreateMany(ctx, indexes)
	})
	return err
}

// ------------------------------------------------------------------
// Chunked upload lifecycle
// ------------------------------------------------------------------

// BeginUpload starts a new chunked upload and returns an upload_id.
func (s *CapturesStore) BeginUpload(ctx context.Context, initialPayload []byte) (string, error) {
	if err := s.ensureIndexes(ctx); err != nil {
		return "", fmt.Errorf("ensure indexes: %w", err)
	}

	uploadID := generateUploadID()
	now := time.Now().UTC()
	expires := now.Add(defaultTTL)

	pu := &PendingUpload{
		UploadID:  uploadID,
		Chunks:    make(map[int]CaptureChunk),
		CreatedAt: now,
		ExpiresAt: expires,
	}

	if len(initialPayload) > 0 {
		pu.Chunks[0] = CaptureChunk{Seq: 0, Data: initialPayload}
	}

	s.pendingMu.Lock()
	s.pending[uploadID] = pu
	s.pendingMu.Unlock()

	return uploadID, nil
}

// AppendChunk adds a chunk to an in-progress upload. seq must be >= 0.
// Returns an error if the upload does not exist or seq is already present.
func (s *CapturesStore) AppendChunk(_ context.Context, uploadID string, seq int, data []byte) error {
	if seq < 0 {
		return errors.New("seq must be non-negative")
	}

	s.pendingMu.RLock()
	pu, ok := s.pending[uploadID]
	s.pendingMu.RUnlock()
	if !ok {
		return fmt.Errorf("upload %q not found", uploadID)
	}

	pu.mu.Lock()
	defer pu.mu.Unlock()

	if _, exists := pu.Chunks[seq]; exists {
		return fmt.Errorf("seq %d already present in upload %q", seq, uploadID)
	}

	pu.Chunks[seq] = CaptureChunk{Seq: seq, Data: data}
	return nil
}

// FinishUpload finalises a chunked upload, writes the assembled capture to
// MongoDB, and removes the pending upload. The returned capture_id is a new
// unique identifier.
func (s *CapturesStore) FinishUpload(ctx context.Context, uploadID string, message string) (string, error) {
	if err := s.ensureIndexes(ctx); err != nil {
		return "", fmt.Errorf("ensure indexes: %w", err)
	}

	s.pendingMu.Lock()
	pu, ok := s.pending[uploadID]
	if ok {
		delete(s.pending, uploadID)
	}
	s.pendingMu.Unlock()

	if !ok {
		return "", fmt.Errorf("upload %q not found", uploadID)
	}

	pu.mu.Lock()
	defer pu.mu.Unlock()

	// Reassemble chunks in order
	seqs := make([]int, 0, len(pu.Chunks))
	for seq := range pu.Chunks {
		seqs = append(seqs, seq)
	}
	sort.Ints(seqs)

	chunks := make([]CaptureChunk, 0, len(seqs))
	for _, seq := range seqs {
		chunks = append(chunks, pu.Chunks[seq])
	}

	captureID := generateCaptureID()
	now := time.Now().UTC()
	expires := now.Add(defaultTTL)

	rec := CaptureRecord{
		CaptureID:  captureID,
		Message:    message,
		Chunks:     chunks,
		CreatedAt:  now,
		ExpiresAt:  expires,
		FinishedAt: &now,
	}

	_, err := s.coll.InsertOne(ctx, rec)
	if err != nil {
		return "", fmt.Errorf("insert capture: %w", err)
	}

	return captureID, nil
}

// ------------------------------------------------------------------
// CRUD on persisted captures
// ------------------------------------------------------------------

// Get retrieves a single completed capture by capture_id.
func (s *CapturesStore) Get(ctx context.Context, captureID string) (*CaptureRecord, error) {
	if err := s.ensureIndexes(ctx); err != nil {
		return nil, fmt.Errorf("ensure indexes: %w", err)
	}

	var rec CaptureRecord
	err := s.coll.FindOne(ctx, bson.M{"capture_id": captureID}).Decode(&rec)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, fmt.Errorf("capture %q not found", captureID)
		}
		return nil, err
	}
	return &rec, nil
}

// Cleanup deletes all documents in the collection (hard delete). Intended for
// tests and manual maintenance.
func (s *CapturesStore) Cleanup(ctx context.Context) error {
	_, err := s.coll.DeleteMany(ctx, bson.M{})
	return err
}

// ------------------------------------------------------------------
// ID generators
// ------------------------------------------------------------------

func generateUploadID() string {
	return fmt.Sprintf("up_%d", time.Now().UTC().UnixNano())
}

func generateCaptureID() string {
	return fmt.Sprintf("cap_%d", time.Now().UTC().UnixNano())
}
