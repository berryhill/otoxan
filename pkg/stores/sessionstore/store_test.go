// Package sessionstore provides a MongoDB-backed store for agent sessions and
// their messages.
package sessionstore

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/v2/bson"
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

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("failed to connect to mongodb: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Disconnect(ctx)
	})

	return client
}

// newTestStore returns a Store backed by a fresh per-agent test DB.
func newTestStore(t *testing.T, client *mongo.Client) *Store {
	t.Helper()
	store, err := NewStore(client, "testagent")
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	return store
}

// ------------------------------------------------------------------
// Session create / get
// ------------------------------------------------------------------

func TestStore_CreateAndGetSession(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	sess := &SessionDoc{
		SessionID: "sess_001",
		Title:     "Test session",
		Status:    SessionStatusActive,
		CreatedBy: "silas",
		ContextData: bson.M{"topic": "testing"},
	}

	res, err := store.CreateSession(ctx, sess)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, res.InsertedID)

	got, err := store.GetSession(ctx, "sess_001")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "sess_001", got.SessionID)
	assert.Equal(t, "Test session", got.Title)
	assert.Equal(t, SessionStatusActive, got.Status)
	assert.Equal(t, "silas", got.CreatedBy)
	assert.Equal(t, "testing", got.ContextData["topic"])
	assert.False(t, got.Deleted)
	assert.Nil(t, got.DeletedAt)
}

func TestStore_GetSession_NotFound(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	_, err := store.GetSession(ctx, "nonexistent")
	assert.Equal(t, mongo.ErrNoDocuments, err)
}

// ------------------------------------------------------------------
// Session update
// ------------------------------------------------------------------

func TestStore_UpdateSession(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	_, err := store.CreateSession(ctx, &SessionDoc{
		SessionID: "sess_upd",
		Title:     "Before",
		Status:    SessionStatusActive,
		CreatedBy: "silas",
	})
	require.NoError(t, err)

	ures, err := store.UpdateSession(ctx, "sess_upd", bson.M{
		"title":  "After",
		"status": SessionStatusPaused,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), ures.ModifiedCount)

	got, err := store.GetSession(ctx, "sess_upd")
	require.NoError(t, err)
	assert.Equal(t, "After", got.Title)
	assert.Equal(t, SessionStatusPaused, got.Status)
	assert.False(t, got.UpdatedAt.IsZero())
}

// ------------------------------------------------------------------
// Session soft-delete / restore / hard-delete
// ------------------------------------------------------------------

func TestStore_SoftDeleteSession(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	_, err := store.CreateSession(ctx, &SessionDoc{
		SessionID: "sess_del",
		Title:     "Delete me",
		Status:    SessionStatusActive,
		CreatedBy: "silas",
	})
	require.NoError(t, err)

	// Append a message first
	_, err = store.AppendMessage(ctx, &MessageDoc{
		MessageID: "msg_001",
		SessionID: "sess_del",
		Role:      MessageRoleUser,
		Content:   "hello",
	})
	require.NoError(t, err)

	dres, err := store.DeleteSession(ctx, "sess_del")
	require.NoError(t, err)
	assert.Equal(t, int64(1), dres.ModifiedCount)

	// Session should be invisible to GetSession
	_, err = store.GetSession(ctx, "sess_del")
	assert.Equal(t, mongo.ErrNoDocuments, err)

	// But visible with IncludeDeleted
	got, err := store.GetSessionWithDeleted(ctx, "sess_del")
	require.NoError(t, err)
	assert.True(t, got.Deleted)
	assert.NotNil(t, got.DeletedAt)

	// Message should also be soft-deleted
	_, err = store.GetMessage(ctx, "msg_001")
	assert.Equal(t, mongo.ErrNoDocuments, err)
}

func TestStore_RestoreSession(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	_, err := store.CreateSession(ctx, &SessionDoc{
		SessionID: "sess_res",
		Title:     "Restore me",
		Status:    SessionStatusActive,
		CreatedBy: "silas",
	})
	require.NoError(t, err)

	_, _ = store.DeleteSession(ctx, "sess_res")

	rres, err := store.RestoreSession(ctx, "sess_res")
	require.NoError(t, err)
	assert.Equal(t, int64(1), rres.ModifiedCount)

	got, err := store.GetSession(ctx, "sess_res")
	require.NoError(t, err)
	assert.False(t, got.Deleted)
	assert.Nil(t, got.DeletedAt)
}

func TestStore_HardDeleteSession(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	_, err := store.CreateSession(ctx, &SessionDoc{
		SessionID: "sess_hard",
		Title:     "Hard delete me",
		Status:    SessionStatusActive,
		CreatedBy: "silas",
	})
	require.NoError(t, err)

	dres, err := store.HardDeleteSession(ctx, "sess_hard")
	require.NoError(t, err)
	assert.Equal(t, int64(1), dres.DeletedCount)

	_, err = store.GetSessionWithDeleted(ctx, "sess_hard")
	assert.Equal(t, mongo.ErrNoDocuments, err)
}

// ------------------------------------------------------------------
// ListSessions
// ------------------------------------------------------------------

func TestStore_ListSessions(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	for i := 0; i < 3; i++ {
		_, err := store.CreateSession(ctx, &SessionDoc{
			SessionID:   fmt.Sprintf("sess_l%d", i),
			Title:       fmt.Sprintf("Session %d", i),
			Status:      SessionStatusActive,
			CreatedBy:   "silas",
			ContextData: bson.M{},
		})
		require.NoError(t, err)
	}

	all, err := store.ListSessions(ctx, ListSessionsOptions{Limit: 10})
	require.NoError(t, err)
	assert.Len(t, all, 3)

	// Should be sorted by created_at desc
	for i := 0; i < len(all)-1; i++ {
		assert.True(t, all[i].CreatedAt.After(all[i+1].CreatedAt) || all[i].CreatedAt.Equal(all[i+1].CreatedAt))
	}
}

func TestStore_ListSessions_WithDeleted(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	_, err := store.CreateSession(ctx, &SessionDoc{
		SessionID: "sess_ld",
		Title:     "List deleted",
		Status:    SessionStatusActive,
		CreatedBy: "silas",
	})
	require.NoError(t, err)

	_, _ = store.DeleteSession(ctx, "sess_ld")

	live, err := store.ListSessions(ctx, ListSessionsOptions{})
	require.NoError(t, err)
	assert.Len(t, live, 0)

	withDeleted, err := store.ListSessions(ctx, ListSessionsOptions{IncludeDeleted: true})
	require.NoError(t, err)
	assert.Len(t, withDeleted, 1)
	assert.Equal(t, "sess_ld", withDeleted[0].SessionID)
}

func TestStore_ListSessions_ByStatus(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	_, err := store.CreateSession(ctx, &SessionDoc{
		SessionID: "sess_a1",
		Title:     "Active",
		Status:    SessionStatusActive,
		CreatedBy: "silas",
	})
	require.NoError(t, err)

	_, err = store.CreateSession(ctx, &SessionDoc{
		SessionID: "sess_c1",
		Title:     "Completed",
		Status:    SessionStatusCompleted,
		CreatedBy: "silas",
	})
	require.NoError(t, err)

	active, err := store.ListSessions(ctx, ListSessionsOptions{Status: []SessionStatus{SessionStatusActive}})
	require.NoError(t, err)
	assert.Len(t, active, 1)
	assert.Equal(t, "sess_a1", active[0].SessionID)

	completed, err := store.ListSessions(ctx, ListSessionsOptions{Status: []SessionStatus{SessionStatusCompleted}})
	require.NoError(t, err)
	assert.Len(t, completed, 1)
	assert.Equal(t, "sess_c1", completed[0].SessionID)
}

// ------------------------------------------------------------------
// Message append / get / ordered list
// ------------------------------------------------------------------

func TestStore_AppendAndGetMessage(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	_, err := store.CreateSession(ctx, &SessionDoc{
		SessionID: "sess_msg",
		Title:     "Message test",
		Status:    SessionStatusActive,
		CreatedBy: "silas",
	})
	require.NoError(t, err)

	res, err := store.AppendMessage(ctx, &MessageDoc{
		MessageID: "msg_a",
		SessionID: "sess_msg",
		Role:      MessageRoleUser,
		Content:   "Hello",
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	got, err := store.GetMessage(ctx, "msg_a")
	require.NoError(t, err)
	assert.Equal(t, "msg_a", got.MessageID)
	assert.Equal(t, "sess_msg", got.SessionID)
	assert.Equal(t, MessageRoleUser, got.Role)
	assert.Equal(t, "Hello", got.Content)
	assert.Equal(t, 1, got.Sequence) // first message = sequence 1
	assert.False(t, got.CreatedAt.IsZero())
}

func TestStore_ListMessages_Ordered(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	_, err := store.CreateSession(ctx, &SessionDoc{
		SessionID: "sess_order",
		Title:     "Order test",
		Status:    SessionStatusActive,
		CreatedBy: "silas",
	})
	require.NoError(t, err)

	// Append messages in order
	for i, content := range []string{"first", "second", "third"} {
		_, err := store.AppendMessage(ctx, &MessageDoc{
			MessageID: fmt.Sprintf("msg_%d", i),
			SessionID: "sess_order",
			Role:      MessageRoleUser,
			Content:   content,
		})
		require.NoError(t, err)
	}

	msgs, err := store.ListMessages(ctx, "sess_order")
	require.NoError(t, err)
	require.Len(t, msgs, 3)

	// Verify ascending sequence order
	assert.Equal(t, 1, msgs[0].Sequence)
	assert.Equal(t, "first", msgs[0].Content)
	assert.Equal(t, 2, msgs[1].Sequence)
	assert.Equal(t, "second", msgs[1].Content)
	assert.Equal(t, 3, msgs[2].Sequence)
	assert.Equal(t, "third", msgs[2].Content)
}

func TestStore_ListMessages_Limit(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	_, err := store.CreateSession(ctx, &SessionDoc{
		SessionID: "sess_lim",
		Title:     "Limit test",
		Status:    SessionStatusActive,
		CreatedBy: "silas",
	})
	require.NoError(t, err)

	for i := 0; i < 5; i++ {
		_, err := store.AppendMessage(ctx, &MessageDoc{
			MessageID: fmt.Sprintf("msg_l%d", i),
			SessionID: "sess_lim",
			Role:      MessageRoleUser,
			Content:   fmt.Sprintf("msg %d", i),
		})
		require.NoError(t, err)
	}

	msgs, err := store.ListMessages(ctx, "sess_lim", WithLimit(2))
	require.NoError(t, err)
	assert.Len(t, msgs, 2)
	assert.Equal(t, 1, msgs[0].Sequence)
	assert.Equal(t, 2, msgs[1].Sequence)
}

// ------------------------------------------------------------------
// Message soft-delete
// ------------------------------------------------------------------

func TestStore_SoftDeleteMessage(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	_, err := store.CreateSession(ctx, &SessionDoc{
		SessionID: "sess_mdel",
		Title:     "Message delete test",
		Status:    SessionStatusActive,
		CreatedBy: "silas",
	})
	require.NoError(t, err)

	_, err = store.AppendMessage(ctx, &MessageDoc{
		MessageID: "msg_del",
		SessionID: "sess_mdel",
		Role:      MessageRoleUser,
		Content:   "delete me",
	})
	require.NoError(t, err)

	dres, err := store.DeleteMessage(ctx, "msg_del")
	require.NoError(t, err)
	assert.Equal(t, int64(1), dres.ModifiedCount)

	// Should be invisible to GetMessage
	_, err = store.GetMessage(ctx, "msg_del")
	assert.Equal(t, mongo.ErrNoDocuments, err)

	// But visible with IncludeDeleted
	got, err := store.GetMessageWithDeleted(ctx, "msg_del")
	require.NoError(t, err)
	assert.True(t, got.Deleted)
	assert.NotNil(t, got.DeletedAt)

	// ListMessages without IncludeDeleted should exclude it
	msgs, err := store.ListMessages(ctx, "sess_mdel")
	require.NoError(t, err)
	assert.Len(t, msgs, 0)

	// ListMessages with IncludeDeleted should include it
	msgs, err = store.ListMessages(ctx, "sess_mdel", WithIncludeDeleted())
	require.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, "msg_del", msgs[0].MessageID)
}

func TestStore_RestoreMessage(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	_, err := store.CreateSession(ctx, &SessionDoc{
		SessionID: "sess_mres",
		Title:     "Message restore test",
		Status:    SessionStatusActive,
		CreatedBy: "silas",
	})
	require.NoError(t, err)

	_, err = store.AppendMessage(ctx, &MessageDoc{
		MessageID: "msg_res",
		SessionID: "sess_mres",
		Role:      MessageRoleUser,
		Content:   "restore me",
	})
	require.NoError(t, err)

	_, _ = store.DeleteMessage(ctx, "msg_res")

	rres, err := store.RestoreMessage(ctx, "msg_res")
	require.NoError(t, err)
	assert.Equal(t, int64(1), rres.ModifiedCount)

	got, err := store.GetMessage(ctx, "msg_res")
	require.NoError(t, err)
	assert.False(t, got.Deleted)
	assert.Nil(t, got.DeletedAt)
}

func TestStore_HardDeleteMessage(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	_, err := store.CreateSession(ctx, &SessionDoc{
		SessionID: "sess_mhard",
		Title:     "Message hard delete test",
		Status:    SessionStatusActive,
		CreatedBy: "silas",
	})
	require.NoError(t, err)

	_, err = store.AppendMessage(ctx, &MessageDoc{
		MessageID: "msg_hard",
		SessionID: "sess_mhard",
		Role:      MessageRoleUser,
		Content:   "hard delete me",
	})
	require.NoError(t, err)

	dres, err := store.HardDeleteMessage(ctx, "msg_hard")
	require.NoError(t, err)
	assert.Equal(t, int64(1), dres.DeletedCount)

	_, err = store.GetMessageWithDeleted(ctx, "msg_hard")
	assert.Equal(t, mongo.ErrNoDocuments, err)
}

// ------------------------------------------------------------------
// Cross-session isolation
// ------------------------------------------------------------------

func TestStore_ListMessages_Isolation(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	for _, sid := range []string{"sess_a", "sess_b"} {
		_, err := store.CreateSession(ctx, &SessionDoc{
			SessionID: sid,
			Title:     sid,
			Status:    SessionStatusActive,
			CreatedBy: "silas",
		})
		require.NoError(t, err)

		_, err = store.AppendMessage(ctx, &MessageDoc{
			MessageID: "msg_" + sid,
			SessionID: sid,
			Role:      MessageRoleUser,
			Content:   "msg for " + sid,
		})
		require.NoError(t, err)
	}

	msgsA, err := store.ListMessages(ctx, "sess_a")
	require.NoError(t, err)
	assert.Len(t, msgsA, 1)
	assert.Equal(t, "msg for sess_a", msgsA[0].Content)

	msgsB, err := store.ListMessages(ctx, "sess_b")
	require.NoError(t, err)
	assert.Len(t, msgsB, 1)
	assert.Equal(t, "msg for sess_b", msgsB[0].Content)
}

// ------------------------------------------------------------------
// Sequence counter correctness
// ------------------------------------------------------------------

func TestStore_SequenceCounter_AutoIncrement(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	_, err := store.CreateSession(ctx, &SessionDoc{
		SessionID: "sess_seq",
		Title:     "Sequence test",
		Status:    SessionStatusActive,
		CreatedBy: "silas",
	})
	require.NoError(t, err)

	// Append 10 messages, verify sequences are 1..10
	for i := 0; i < 10; i++ {
		res, err := store.AppendMessage(ctx, &MessageDoc{
			MessageID: fmt.Sprintf("msg_seq%d", i),
			SessionID: "sess_seq",
			Role:      MessageRoleUser,
			Content:   fmt.Sprintf("msg %d", i),
		})
		require.NoError(t, err)
		require.NotNil(t, res)
	}

	msgs, err := store.ListMessages(ctx, "sess_seq")
	require.NoError(t, err)
	require.Len(t, msgs, 10)

	for i, msg := range msgs {
		assert.Equal(t, i+1, msg.Sequence, "message %d should have sequence %d", i, i+1)
	}
}

func TestStore_SequenceCounter_PerSessionIsolation(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	for _, sid := range []string{"sess_x", "sess_y"} {
		_, err := store.CreateSession(ctx, &SessionDoc{
			SessionID: sid,
			Title:     sid,
			Status:    SessionStatusActive,
			CreatedBy: "silas",
		})
		require.NoError(t, err)
	}

	// Append 3 messages to each session
	for _, sid := range []string{"sess_x", "sess_y"} {
		for i := 0; i < 3; i++ {
			_, err := store.AppendMessage(ctx, &MessageDoc{
				MessageID: fmt.Sprintf("msg_%s_%d", sid, i),
				SessionID: sid,
				Role:      MessageRoleUser,
				Content:   fmt.Sprintf("msg %d", i),
			})
			require.NoError(t, err)
		}
	}

	msgsX, err := store.ListMessages(ctx, "sess_x")
	require.NoError(t, err)
	require.Len(t, msgsX, 3)
	for i, msg := range msgsX {
		assert.Equal(t, i+1, msg.Sequence)
	}

	msgsY, err := store.ListMessages(ctx, "sess_y")
	require.NoError(t, err)
	require.Len(t, msgsY, 3)
	for i, msg := range msgsY {
		assert.Equal(t, i+1, msg.Sequence)
	}
}

// ------------------------------------------------------------------
// Validation
// ------------------------------------------------------------------

func TestStore_AppendMessage_RequiresSessionID(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	_, err := store.AppendMessage(ctx, &MessageDoc{
		MessageID: "msg_nosess",
		Role:      MessageRoleUser,
		Content:   "no session",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session_id is required")
}

func TestStore_AppendMessage_RequiresMessageID(t *testing.T) {
	ctx := context.Background()
	client := setupMongo(t)
	store := newTestStore(t, client)

	_, err := store.AppendMessage(ctx, &MessageDoc{
		SessionID: "sess_val",
		Role:      MessageRoleUser,
		Content:   "no message id",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "message_id is required")
}
