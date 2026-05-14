// Package index provides the indexer main loop that polls MongoDB for new or
// updated documents, embeds them, writes vectors to Qdrant, and records pointer
// documents so that re-indexing and deletion propagation are handled.
package index

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/silas/otoxan/internal/qdrant"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// ------------------------------------------------------------------
// Config
// ------------------------------------------------------------------

// IndexerConfig tunes the indexer behaviour.
type IndexerConfig struct {
	// BatchSize is the maximum number of documents to embed in a single call.
	BatchSize int
	// PollInterval is how long to wait between full polling cycles.
	PollInterval time.Duration
	// VectorSize is the dimension of the embedding vectors (must match the
	// embedder and the Qdrant collection).
	VectorSize int
	// MaxRetries is the number of times to retry a Qdrant operation on a
	// transient error before giving up on the current cycle.
	MaxRetries int
	// RetryBase is the initial backoff duration before the first retry.
	RetryBase time.Duration
	// RetryMax is the maximum backoff duration between retries.
	RetryMax time.Duration
}

// DefaultIndexerConfig returns sensible defaults.
func DefaultIndexerConfig() IndexerConfig {
	return IndexerConfig{
		BatchSize:    32,
		PollInterval: 60 * time.Second,
		VectorSize:   1536,
		MaxRetries:   5,
		RetryBase:    500 * time.Millisecond,
		RetryMax:     30 * time.Second,
	}
}

// Embedder is the interface for text embedding backends.
type Embedder interface {
	// Embed returns a dense vector for the given text.
	Embed(ctx context.Context, text string) ([]float32, error)
	// BatchEmbed returns vectors for multiple texts in a single call.
	BatchEmbed(ctx context.Context, texts []string) ([][]float32, error)
	// Model returns the model name used by this embedder.
	Model() string
	// Dimension returns the vector dimension.
	Dimension() int
}

// ------------------------------------------------------------------
// Source configuration
// ------------------------------------------------------------------

// SourceConfig describes one MongoDB collection that the indexer should watch.
type SourceConfig struct {
	// CollectionName is the MongoDB collection name (e.g. "plans").
	CollectionName string
	// SourceType is the payload tag written to Qdrant (e.g. "plan").
	SourceType string
	// Extractor turns a bson.M document into deterministic text.
	Extractor func(doc bson.M) string
}

// ------------------------------------------------------------------
// Indexer
// ------------------------------------------------------------------

// Indexer polls MongoDB, embeds documents, and writes vectors to Qdrant.
type Indexer struct {
	config       IndexerConfig
	qdrant       *qdrant.Client
	embedder     Embedder
	pointerStore *PointerStore
	sources      []SourceConfig
	qdrantColl   string
}

// NewIndexer creates an Indexer. The caller must supply a Qdrant client, an
// embedder implementation, a pointer store, and the list of source collections
// to index.  qdrantColl is the Qdrant collection name (e.g. "agent_42_index").
func NewIndexer(
	cfg IndexerConfig,
	qc *qdrant.Client,
	emb Embedder,
	ps *PointerStore,
	sources []SourceConfig,
	qdrantColl string,
) *Indexer {
	return &Indexer{
		config:       cfg,
		qdrant:       qc,
		embedder:     emb,
		pointerStore: ps,
		sources:      sources,
		qdrantColl:   qdrantColl,
	}
}

// EnsureCollection creates the Qdrant collection if it does not already exist.
func (ix *Indexer) EnsureCollection(ctx context.Context) error {
	return ix.withRetry(ctx, func() error {
		return ix.qdrant.CreateCollection(ctx, ix.qdrantColl, ix.config.VectorSize)
	})
}

// RunOnce performs a single indexing cycle: find new/updated/stale/deleted docs,
// embed them, upsert to Qdrant, and update pointer docs.
// Transient Qdrant errors are retried with exponential backoff; non-transient
// errors or exhausted retries abort the cycle for that source but the indexer
// keeps running (the caller should not treat the returned error as fatal).
func (ix *Indexer) RunOnce(ctx context.Context) error {
	var firstErr error
	for _, src := range ix.sources {
		if err := ix.indexSource(ctx, src); err != nil {
			log.Printf("[indexer] source %q index failed: %v", src.CollectionName, err)
			if firstErr == nil {
				firstErr = fmt.Errorf("index source %q: %w", src.CollectionName, err)
			}
			continue
		}
		if err := ix.removeDeleted(ctx, src); err != nil {
			log.Printf("[indexer] source %q remove-deleted failed: %v", src.CollectionName, err)
			if firstErr == nil {
				firstErr = fmt.Errorf("remove deleted %q: %w", src.CollectionName, err)
			}
			continue
		}
	}
	return firstErr
}

// Backfill indexes every document in every source collection from scratch,
// regardless of existing pointers. It prints progress logs every N docs.
// This is useful for populating a fresh Qdrant from an existing MongoDB.
func (ix *Indexer) Backfill(ctx context.Context, progressEvery int) error {
	for _, src := range ix.sources {
		if err := ix.backfillSource(ctx, src, progressEvery); err != nil {
			return fmt.Errorf("backfill source %q: %w", src.CollectionName, err)
		}
	}
	return nil
}

// withRetry executes op with exponential backoff on transient Qdrant errors.
// It logs each backoff and clears the error once the operation succeeds.
func (ix *Indexer) withRetry(ctx context.Context, op func() error) error {
	var lastErr error
	backoff := ix.config.RetryBase
	for attempt := 0; attempt <= ix.config.MaxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("[indexer] retry %d/%d after %v (transient error: %v)", attempt, ix.config.MaxRetries, backoff, lastErr)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return ctx.Err()
			}
			// Exponential backoff with cap.
			backoff *= 2
			if backoff > ix.config.RetryMax {
				backoff = ix.config.RetryMax
			}
		}
		err := op()
		if err == nil {
			if attempt > 0 {
				log.Printf("[indexer] recovered after %d retries", attempt)
			}
			return nil
		}
		lastErr = err
		if !qdrant.IsTransientError(err) {
			return err
		}
	}
	return fmt.Errorf("exhausted %d retries: %w", ix.config.MaxRetries, lastErr)
}

// ------------------------------------------------------------------
// Per-source indexing
// ------------------------------------------------------------------

func (ix *Indexer) indexSource(ctx context.Context, src SourceConfig) error {
	db := ix.pointerStore.coll.Database()
	srcColl := db.Collection(src.CollectionName)

	// 1. Load all existing pointers for this source type so we can detect new vs updated.
	filter := bson.M{"source_type": src.SourceType, "removed": bson.M{"$ne": true}}
	cur, err := ix.pointerStore.coll.Find(ctx, filter)
	if err != nil {
		return fmt.Errorf("list pointers: %w", err)
	}
	defer cur.Close(ctx)

	var pointers []MemoryPointer
	if err := cur.All(ctx, &pointers); err != nil {
		return fmt.Errorf("decode pointers: %w", err)
	}

	pointerBySource := make(map[string]MemoryPointer, len(pointers))
	for _, p := range pointers {
		pointerBySource[p.SourceID] = p
	}

	// 2. Scan the source collection for documents that are not soft-deleted.
	sdFilter := bson.M{"deleted": bson.M{"$ne": true}}
	srcCur, err := srcColl.Find(ctx, sdFilter)
	if err != nil {
		return fmt.Errorf("scan source collection: %w", err)
	}
	defer srcCur.Close(ctx)

	var toIndex []indexJob
	for srcCur.Next(ctx) {
		var doc bson.M
		if err := srcCur.Decode(&doc); err != nil {
			return fmt.Errorf("decode source doc: %w", err)
		}

		sourceID := extractStringField(doc, "_id")
		if sourceID == "" {
			sourceID = extractStringField(doc, src.SourceType+"_id")
		}
		if sourceID == "" {
			continue // cannot index without an id
		}

		updatedAt := extractTimeField(doc, "updated_at")

		existing, ok := pointerBySource[sourceID]
		if ok && !updatedAt.IsZero() && !existing.SourceUpdatedAt.Before(updatedAt) {
			// Already indexed and not stale.
			continue
		}

		text := src.Extractor(doc)
		if text == "" {
			continue
		}

		toIndex = append(toIndex, indexJob{
			sourceID:   sourceID,
			sourceType: src.SourceType,
			srcColl:    src.CollectionName,
			text:       text,
			updatedAt:  updatedAt,
			existing:   existing,
		})
	}
	if err := srcCur.Err(); err != nil {
		return fmt.Errorf("cursor error: %w", err)
	}

	// 3. Batch embed and upsert.
	for len(toIndex) > 0 {
		batch := toIndex
		if len(batch) > ix.config.BatchSize {
			batch = batch[:ix.config.BatchSize]
		}
		toIndex = toIndex[len(batch):]

		texts := make([]string, len(batch))
		for i, j := range batch {
			texts[i] = j.text
		}

		vecs, err := ix.embedder.BatchEmbed(ctx, texts)
		if err != nil {
			return fmt.Errorf("batch embed: %w", err)
		}
		if len(vecs) != len(batch) {
			return fmt.Errorf("embedder returned %d vectors for %d texts", len(vecs), len(batch))
		}

		points := make([]qdrant.Point, len(batch))
		for i, j := range batch {
			pointID := uuid.New().String()
			if j.existing.QdrantPointID != "" {
				// Re-use the same Qdrant point ID so we overwrite rather than create a duplicate.
				pointID = j.existing.QdrantPointID
			}
			points[i] = qdrant.Point{
				ID:     pointID,
				Vector: vecs[i],
				Payload: map[string]interface{}{
					"source_type":      j.sourceType,
					"source_id":        j.sourceID,
					"source_collection": j.srcColl,
					"content":          j.text,
				},
			}
		}

		if err := ix.withRetry(ctx, func() error {
			return ix.qdrant.Upsert(ctx, ix.qdrantColl, points)
		}); err != nil {
			return fmt.Errorf("qdrant upsert: %w", err)
		}

		// 4. Write pointer documents.
		for i, j := range batch {
			pointerID := j.existing.PointerID
			if pointerID == "" {
				pointerID = uuid.New().String()
			}
			p := &MemoryPointer{
				PointerID:        pointerID,
				SourceID:         j.sourceID,
				SourceType:       j.sourceType,
				SourceCollection: j.srcColl,
				QdrantPointID:    points[i].ID.(string),
				QdrantCollection: ix.qdrantColl,
				SourceUpdatedAt:  j.updatedAt,
			}
			if _, err := ix.pointerStore.Upsert(ctx, p); err != nil {
				return fmt.Errorf("pointer upsert: %w", err)
			}
		}
	}

	return nil
}

// indexJob holds the work item for a single document that needs indexing.
type indexJob struct {
	sourceID   string
	sourceType string
	srcColl    string
	text       string
	updatedAt  time.Time
	existing   MemoryPointer
}

// ------------------------------------------------------------------
// Backfill (index everything from scratch)
// ------------------------------------------------------------------

func (ix *Indexer) backfillSource(ctx context.Context, src SourceConfig, progressEvery int) error {
	db := ix.pointerStore.coll.Database()
	srcColl := db.Collection(src.CollectionName)

	// 1. Load all existing pointers for this source type so we can reuse IDs.
	filter := bson.M{"source_type": src.SourceType}
	cur, err := ix.pointerStore.coll.Find(ctx, filter)
	if err != nil {
		return fmt.Errorf("list pointers: %w", err)
	}
	defer cur.Close(ctx)

	var pointers []MemoryPointer
	if err := cur.All(ctx, &pointers); err != nil {
		return fmt.Errorf("decode pointers: %w", err)
	}

	pointerBySource := make(map[string]MemoryPointer, len(pointers))
	for _, p := range pointers {
		pointerBySource[p.SourceID] = p
	}

	// 2. Scan the source collection for documents that are not soft-deleted.
	sdFilter := bson.M{"deleted": bson.M{"$ne": true}}
	srcCur, err := srcColl.Find(ctx, sdFilter)
	if err != nil {
		return fmt.Errorf("scan source collection: %w", err)
	}
	defer srcCur.Close(ctx)

	var toIndex []indexJob
	var totalDocs int
	for srcCur.Next(ctx) {
		var doc bson.M
		if err := srcCur.Decode(&doc); err != nil {
			return fmt.Errorf("decode source doc: %w", err)
		}

		sourceID := extractStringField(doc, "_id")
		if sourceID == "" {
			sourceID = extractStringField(doc, src.SourceType+"_id")
		}
		if sourceID == "" {
			continue // cannot index without an id
		}

		updatedAt := extractTimeField(doc, "updated_at")
		text := src.Extractor(doc)
		if text == "" {
			continue
		}

		toIndex = append(toIndex, indexJob{
			sourceID:   sourceID,
			sourceType: src.SourceType,
			srcColl:    src.CollectionName,
			text:       text,
			updatedAt:  updatedAt,
			existing:   pointerBySource[sourceID],
		})
		totalDocs++
	}
	if err := srcCur.Err(); err != nil {
		return fmt.Errorf("cursor error: %w", err)
	}

	log.Printf("[backfill] %s: %d documents to index", src.CollectionName, totalDocs)

	// 3. Batch embed and upsert.
	var processed int
	for len(toIndex) > 0 {
		batch := toIndex
		if len(batch) > ix.config.BatchSize {
			batch = batch[:ix.config.BatchSize]
		}
		toIndex = toIndex[len(batch):]

		texts := make([]string, len(batch))
		for i, j := range batch {
			texts[i] = j.text
		}

		vecs, err := ix.embedder.BatchEmbed(ctx, texts)
		if err != nil {
			return fmt.Errorf("batch embed: %w", err)
		}
		if len(vecs) != len(batch) {
			return fmt.Errorf("embedder returned %d vectors for %d texts", len(vecs), len(batch))
		}

		points := make([]qdrant.Point, len(batch))
		for i, j := range batch {
			pointID := uuid.New().String()
			if j.existing.QdrantPointID != "" {
				// Re-use the same Qdrant point ID so we overwrite rather than create a duplicate.
				pointID = j.existing.QdrantPointID
			}
			points[i] = qdrant.Point{
				ID:     pointID,
				Vector: vecs[i],
				Payload: map[string]interface{}{
					"source_type":       j.sourceType,
					"source_id":         j.sourceID,
					"source_collection": j.srcColl,
				},
			}
		}

		if err := ix.withRetry(ctx, func() error {
			return ix.qdrant.Upsert(ctx, ix.qdrantColl, points)
		}); err != nil {
			return fmt.Errorf("qdrant upsert: %w", err)
		}

		// 4. Write pointer documents (reuse pointer_id if exists).
		for i, j := range batch {
			pointerID := j.existing.PointerID
			if pointerID == "" {
				pointerID = uuid.New().String()
			}
			p := &MemoryPointer{
				PointerID:        pointerID,
				SourceID:         j.sourceID,
				SourceType:       j.sourceType,
				SourceCollection: j.srcColl,
				QdrantPointID:    points[i].ID.(string),
				QdrantCollection: ix.qdrantColl,
				SourceUpdatedAt:  j.updatedAt,
			}
			if _, err := ix.pointerStore.Upsert(ctx, p); err != nil {
				return fmt.Errorf("pointer upsert: %w", err)
			}
		}

		processed += len(batch)
		if progressEvery > 0 && processed%progressEvery == 0 {
			log.Printf("[backfill] %s: processed %d/%d docs", src.CollectionName, processed, totalDocs)
		}
	}

	log.Printf("[backfill] %s: complete — %d docs indexed", src.CollectionName, processed)
	return nil
}

// ------------------------------------------------------------------
// Deletion propagation
// ------------------------------------------------------------------

func (ix *Indexer) removeDeleted(ctx context.Context, src SourceConfig) error {
	// Find pointers for this source type that are NOT marked removed.
	filter := bson.M{"source_type": src.SourceType, "removed": bson.M{"$ne": true}}
	cur, err := ix.pointerStore.coll.Find(ctx, filter)
	if err != nil {
		return fmt.Errorf("list pointers: %w", err)
	}
	defer cur.Close(ctx)

	var pointers []MemoryPointer
	if err := cur.All(ctx, &pointers); err != nil {
		return fmt.Errorf("decode pointers: %w", err)
	}

	db := ix.pointerStore.coll.Database()
	srcColl := db.Collection(src.CollectionName)

	var toDelete []MemoryPointer
	for _, p := range pointers {
		// Check whether the source document still exists and is not soft-deleted.
		var idField string
		switch src.SourceType {
		case "plan":
			idField = "plan_id"
		case "task":
			idField = "task_id"
		case "report":
			idField = "report_id"
		case "directive":
			idField = "directive_id"
		case "session":
			idField = "session_id"
		case "build":
			idField = "build_id"
		case "error":
			idField = "error_id"
		case "run":
			idField = "run_id"
		case "task_event":
			idField = "event_id"
		case "notification":
			idField = "notification_id"
		case "flow":
			idField = "flow_id"
		default:
			idField = "_id"
		}

		count, err := srcColl.CountDocuments(ctx, bson.M{
			idField:   p.SourceID,
			"deleted": bson.M{"$ne": true},
		})
		if err != nil {
			return fmt.Errorf("count source doc %s: %w", p.SourceID, err)
		}
		if count == 0 {
			toDelete = append(toDelete, p)
		}
	}

	if len(toDelete) == 0 {
		return nil
	}

	// Delete from Qdrant.
	ids := make([]string, len(toDelete))
	for i, p := range toDelete {
		ids[i] = p.QdrantPointID
	}
	if err := ix.withRetry(ctx, func() error {
		return ix.qdrant.DeletePoints(ctx, ix.qdrantColl, ids)
	}); err != nil {
		return fmt.Errorf("qdrant delete: %w", err)
	}

	// Mark pointers removed.
	for _, p := range toDelete {
		if _, err := ix.pointerStore.MarkRemoved(ctx, p.PointerID); err != nil {
			return fmt.Errorf("mark removed %s: %w", p.PointerID, err)
		}
	}

	return nil
}

// ------------------------------------------------------------------
// Helpers
// ------------------------------------------------------------------

func extractStringField(doc bson.M, key string) string {
	v, ok := doc[key]
	if !ok {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case *string:
		if val == nil {
			return ""
		}
		return *val
	default:
		return ""
	}
}

func extractTimeField(doc bson.M, key string) time.Time {
	v, ok := doc[key]
	if !ok {
		return time.Time{}
	}
	switch val := v.(type) {
	case time.Time:
		return val
	case *time.Time:
		if val == nil {
			return time.Time{}
		}
		return *val
	default:
		return time.Time{}
	}
}
