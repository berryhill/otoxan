package companion

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// setupMongo spins up a testcontainers MongoDB container and returns a client.
func setupMongo(t *testing.T) *mongo.Client {
	t.Helper()
	ctx := context.Background()

	container, err := mongodb.Run(ctx, "mongo:7")
	if err != nil {
		t.Fatalf("failed to start mongodb container: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(ctx)
	})

	uri, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get connection string: %v", err)
	}
	os.Setenv("MONGO_URI", uri)

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("failed to connect to mongodb: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Disconnect(ctx)
	})

	return client
}

// newTestStore returns a CapturesStore backed by a fresh test collection.
func newTestStore(t *testing.T, client *mongo.Client) *CapturesStore {
	t.Helper()
	db := client.Database("silas")
	return NewCapturesStore(db)
}

// ------------------------------------------------------------------
// Happy path
// ------------------------------------------------------------------

func TestCapturesStore_BeginUpload(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	uploadID, err := store.BeginUpload(ctx, []byte("initial"))
	require.NoError(t, err)
	assert.NotEmpty(t, uploadID)
	assert.True(t, strings.HasPrefix(uploadID, "up_"))
}

func TestCapturesStore_FullChunkedUpload(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	// 1. Begin upload with initial payload
	uploadID, err := store.BeginUpload(ctx, []byte("chunk-0"))
	require.NoError(t, err)

	// 2. Append more chunks
	for i := 1; i <= 3; i++ {
		err := store.AppendChunk(ctx, uploadID, i, []byte(fmt.Sprintf("chunk-%d", i)))
		require.NoError(t, err)
	}

	// 3. Finish upload
	captureID, err := store.FinishUpload(ctx, uploadID, "test capture message")
	require.NoError(t, err)
	assert.NotEmpty(t, captureID)
	assert.True(t, strings.HasPrefix(captureID, "cap_"))

	// 4. Read back
	rec, err := store.Get(ctx, captureID)
	require.NoError(t, err)
	assert.Equal(t, captureID, rec.CaptureID)
	assert.Equal(t, "test capture message", rec.Message)
	assert.Len(t, rec.Chunks, 4)
	assert.Equal(t, 0, rec.Chunks[0].Seq)
	assert.Equal(t, []byte("chunk-0"), rec.Chunks[0].Data)
	assert.Equal(t, 3, rec.Chunks[3].Seq)
	assert.Equal(t, []byte("chunk-3"), rec.Chunks[3].Data)
	assert.NotNil(t, rec.FinishedAt)
	assert.False(t, rec.CreatedAt.IsZero())
	assert.False(t, rec.ExpiresAt.IsZero())
}

func TestCapturesStore_BeginUpload_NoInitialPayload(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	uploadID, err := store.BeginUpload(ctx, nil)
	require.NoError(t, err)

	// Append first chunk manually
	err = store.AppendChunk(ctx, uploadID, 0, []byte("first"))
	require.NoError(t, err)

	captureID, err := store.FinishUpload(ctx, uploadID, "no initial")
	require.NoError(t, err)

	rec, err := store.Get(ctx, captureID)
	require.NoError(t, err)
	assert.Len(t, rec.Chunks, 1)
	assert.Equal(t, []byte("first"), rec.Chunks[0].Data)
}

// ------------------------------------------------------------------
// Chunk out of order / duplicate seq
// ------------------------------------------------------------------

func TestCapturesStore_ChunkOutOfOrder(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	uploadID, err := store.BeginUpload(ctx, []byte("chunk-0"))
	require.NoError(t, err)

	// Append chunks out of order
	err = store.AppendChunk(ctx, uploadID, 3, []byte("chunk-3"))
	require.NoError(t, err)
	err = store.AppendChunk(ctx, uploadID, 1, []byte("chunk-1"))
	require.NoError(t, err)
	err = store.AppendChunk(ctx, uploadID, 2, []byte("chunk-2"))
	require.NoError(t, err)

	captureID, err := store.FinishUpload(ctx, uploadID, "out of order")
	require.NoError(t, err)

	rec, err := store.Get(ctx, captureID)
	require.NoError(t, err)
	assert.Len(t, rec.Chunks, 4)
	// Verify reassembly sorted by seq
	for i := 0; i < 4; i++ {
		assert.Equal(t, i, rec.Chunks[i].Seq)
		assert.Equal(t, []byte(fmt.Sprintf("chunk-%d", i)), rec.Chunks[i].Data)
	}
}

func TestCapturesStore_DuplicateSeqRejected(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	uploadID, err := store.BeginUpload(ctx, []byte("chunk-0"))
	require.NoError(t, err)

	// seq 1 is new
	err = store.AppendChunk(ctx, uploadID, 1, []byte("chunk-1"))
	require.NoError(t, err)

	// seq 1 again should fail
	err = store.AppendChunk(ctx, uploadID, 1, []byte("duplicate"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already present")
}

func TestCapturesStore_NegativeSeqRejected(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	uploadID, err := store.BeginUpload(ctx, nil)
	require.NoError(t, err)

	err = store.AppendChunk(ctx, uploadID, -1, []byte("bad"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-negative")
}

// ------------------------------------------------------------------
// Upload not found errors
// ------------------------------------------------------------------

func TestCapturesStore_AppendChunk_UnknownUpload(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	err := store.AppendChunk(ctx, "up_nonexistent", 0, []byte("x"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestCapturesStore_FinishUpload_UnknownUpload(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	_, err := store.FinishUpload(ctx, "up_nonexistent", "msg")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestCapturesStore_Get_NotFound(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	_, err := store.Get(ctx, "cap_nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ------------------------------------------------------------------
// Cleanup
// ------------------------------------------------------------------

func TestCapturesStore_Cleanup(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	uploadID, err := store.BeginUpload(ctx, []byte("x"))
	require.NoError(t, err)
	captureID, err := store.FinishUpload(ctx, uploadID, "cleanup test")
	require.NoError(t, err)

	// Verify exists
	_, err = store.Get(ctx, captureID)
	require.NoError(t, err)

	// Cleanup
	err = store.Cleanup(ctx)
	require.NoError(t, err)

	// Verify gone
	_, err = store.Get(ctx, captureID)
	require.Error(t, err)
}

// ------------------------------------------------------------------
// TTL / expiration sweep
// ------------------------------------------------------------------

func TestCapturesStore_TTLExpiration(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	// Insert a capture with a 1-second TTL
	now := time.Now().UTC()
	rec := CaptureRecord{
		CaptureID: "cap_ttl_test",
		Message:   "ttl test",
		Chunks:    []CaptureChunk{{Seq: 0, Data: []byte("x")}},
		CreatedAt: now,
		ExpiresAt: now.Add(1 * time.Second),
	}

	_, err := store.coll.InsertOne(ctx, rec)
	require.NoError(t, err)

	// Verify exists immediately
	got, err := store.Get(ctx, "cap_ttl_test")
	require.NoError(t, err)
	assert.Equal(t, "cap_ttl_test", got.CaptureID)

	// Wait for TTL sweep (MongoDB TTL monitor runs every 60s by default,
	// but testcontainers with mongo:7 often sweeps faster in practice.
	// We wait 65s to be safe.)
	t.Log("waiting 65s for TTL sweep...")
	time.Sleep(65 * time.Second)

	_, err = store.Get(ctx, "cap_ttl_test")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
