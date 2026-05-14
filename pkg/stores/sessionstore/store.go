// Package sessionstore provides a MongoDB-backed store for agent sessions and
// their messages. Sessions live in the per-agent DB (sessions collection);
// messages live in the same DB (session_messages collection). Both collections
// use soft-delete semantics.
package sessionstore

import (
	"context"
	"fmt"
	"time"

	"github.com/silas/otoxan/internal/softdelete"
	"github.com/silas/otoxan/internal/state"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ------------------------------------------------------------------
// Document models
// ------------------------------------------------------------------

// SessionStatus enumerates the possible states of a session.
type SessionStatus string

const (
	SessionStatusActive    SessionStatus = "ACTIVE"
	SessionStatusPaused    SessionStatus = "PAUSED"
	SessionStatusCompleted SessionStatus = "COMPLETED"
	SessionStatusAbandoned SessionStatus = "ABANDONED"
)

// SessionDoc is the canonical BSON shape for a session.
type SessionDoc struct {
	SessionID   string        `bson:"session_id" json:"session_id"`
	Title       string        `bson:"title" json:"title"`
	Status      SessionStatus `bson:"status" json:"status"`
	CreatedAt   time.Time     `bson:"created_at" json:"created_at"`
	UpdatedAt   time.Time     `bson:"updated_at" json:"updated_at"`
	CreatedBy   string        `bson:"created_by" json:"created_by"`
	ContextData bson.M        `bson:"context_data,omitempty" json:"context_data,omitempty"`

	// Soft-delete fields (managed by softdelete package)
	Deleted   bool       `bson:"deleted,omitempty" json:"deleted,omitempty"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}

// MessageRole enumerates the possible roles of a message sender.
type MessageRole string

const (
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
	MessageRoleSystem    MessageRole = "system"
	MessageRoleTool      MessageRole = "tool"
)

// MessageDoc is the canonical BSON shape for a session message.
type MessageDoc struct {
	MessageID string      `bson:"message_id" json:"message_id"`
	SessionID string      `bson:"session_id" json:"session_id"`
	Role      MessageRole `bson:"role" json:"role"`
	Content   string      `bson:"content" json:"content"`
	Sequence  int         `bson:"sequence" json:"sequence"`
	CreatedAt time.Time   `bson:"created_at" json:"created_at"`
	Metadata  bson.M      `bson:"metadata,omitempty" json:"metadata,omitempty"`

	// Soft-delete fields (managed by softdelete package)
	Deleted   bool       `bson:"deleted,omitempty" json:"deleted,omitempty"`
	DeletedAt *time.Time `bson:"deleted_at,omitempty" json:"deleted_at,omitempty"`
}

// ------------------------------------------------------------------
// Store construction
// ------------------------------------------------------------------

// NewStore creates a session store for the named agent.
// It ensures required indexes on sessions and session_messages collections.
func NewStore(client *mongo.Client, agentName string) (*Store, error) {
	if err := state.ValidateAgentName(agentName); err != nil {
		return nil, err
	}
	db, err := state.AgentDB(client, agentName)
	if err != nil {
		return nil, err
	}
	sessionsColl := db.Collection("sessions")
	messagesColl := db.Collection("session_messages")

	s := &Store{
		agentName:    agentName,
		sessions:     softdelete.NewSoftDelete(sessionsColl),
		messages:     softdelete.NewSoftDelete(messagesColl),
		messagesRaw:  messagesColl,
	}
	if err := s.ensureIndexes(context.Background()); err != nil {
		return nil, fmt.Errorf("ensure indexes: %w", err)
	}
	return s, nil
}

// Store is a MongoDB-backed session store for a single agent.
type Store struct {
	agentName   string
	sessions    *softdelete.SoftDeleteCollection
	messages    *softdelete.SoftDeleteCollection
	messagesRaw *mongo.Collection // raw collection for sequence counter aggregation
}

// ------------------------------------------------------------------
// Indexes
// ------------------------------------------------------------------

func (s *Store) ensureIndexes(ctx context.Context) error {
	// Sessions indexes
	sessionIndexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "session_id", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "status", Value: 1}}},
		{Keys: bson.D{{Key: "created_at", Value: 1}}},
		{Keys: bson.D{{Key: "created_by", Value: 1}}},
	}
	if _, err := s.sessions.Database().Collection(s.sessions.Name()).Indexes().CreateMany(ctx, sessionIndexes); err != nil {
		return fmt.Errorf("sessions indexes: %w", err)
	}

	// Messages indexes
	messageIndexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "session_id", Value: 1}, {Key: "sequence", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "session_id", Value: 1}}},
		{Keys: bson.D{{Key: "created_at", Value: 1}}},
	}
	if _, err := s.messages.Database().Collection(s.messages.Name()).Indexes().CreateMany(ctx, messageIndexes); err != nil {
		return fmt.Errorf("session_messages indexes: %w", err)
	}

	return nil
}

// ------------------------------------------------------------------
// Session CRUD
// ------------------------------------------------------------------

// CreateSession creates a new session document.
func (s *Store) CreateSession(ctx context.Context, doc *SessionDoc) (*mongo.InsertOneResult, error) {
	now := time.Now().UTC()
	if doc.CreatedAt.IsZero() {
		doc.CreatedAt = now
	}
	if doc.UpdatedAt.IsZero() {
		doc.UpdatedAt = now
	}
	if doc.Status == "" {
		doc.Status = SessionStatusActive
	}
	return s.sessions.InsertOne(ctx, doc)
}

// GetSession retrieves a single session by session_id.
func (s *Store) GetSession(ctx context.Context, sessionID string) (*SessionDoc, error) {
	sr := s.sessions.FindOne(ctx, bson.M{"session_id": sessionID})
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var sess SessionDoc
	if err := sr.Decode(&sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

// GetSessionWithDeleted retrieves a session including soft-deleted ones.
func (s *Store) GetSessionWithDeleted(ctx context.Context, sessionID string) (*SessionDoc, error) {
	sr := s.sessions.FindOne(ctx, bson.M{"session_id": sessionID}, softdelete.WithIncludeDeleted())
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var sess SessionDoc
	if err := sr.Decode(&sess); err != nil {
		return nil, err
	}
	return &sess, nil
}

// UpdateSession patches fields of an existing session. Sets updated_at automatically.
func (s *Store) UpdateSession(ctx context.Context, sessionID string, updates bson.M) (*mongo.UpdateResult, error) {
	updates["updated_at"] = time.Now().UTC()
	return s.sessions.UpdateOne(ctx, bson.M{"session_id": sessionID}, bson.M{"$set": updates})
}

// DeleteSession soft-deletes a session and all its messages.
func (s *Store) DeleteSession(ctx context.Context, sessionID string) (*mongo.UpdateResult, error) {
	// Soft-delete all messages in the session
	_, _ = s.messages.DeleteMany(ctx, bson.M{"session_id": sessionID})
	return s.sessions.Delete(ctx, bson.M{"session_id": sessionID})
}

// RestoreSession un-deletes a soft-deleted session.
func (s *Store) RestoreSession(ctx context.Context, sessionID string) (*mongo.UpdateResult, error) {
	return s.sessions.Restore(ctx, bson.M{"session_id": sessionID})
}

// HardDeleteSession permanently removes a session and all its messages.
func (s *Store) HardDeleteSession(ctx context.Context, sessionID string) (*mongo.DeleteResult, error) {
	_, _ = s.messagesRaw.DeleteMany(ctx, bson.M{"session_id": sessionID})
	return s.sessions.HardDelete(ctx, bson.M{"session_id": sessionID})
}

// ListSessions returns sessions matching the provided filters, sorted by created_at descending.
type ListSessionsOptions struct {
	Status         []SessionStatus
	CreatedBy      string
	Limit          int
	IncludeDeleted bool
}

// ListSessions returns sessions matching the provided filters.
func (s *Store) ListSessions(ctx context.Context, opts ListSessionsOptions) ([]SessionDoc, error) {
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
	if opts.CreatedBy != "" {
		filter["created_by"] = opts.CreatedBy
	}

	if !opts.IncludeDeleted {
		filter["deleted"] = bson.M{"$ne": true}
	}

	findOpts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}})
	if opts.Limit > 0 {
		findOpts.SetLimit(int64(opts.Limit))
	}

	cur, err := s.sessions.Database().Collection(s.sessions.Name()).Find(ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var sessions []SessionDoc
	if err := cur.All(ctx, &sessions); err != nil {
		return nil, err
	}
	return sessions, nil
}

// ------------------------------------------------------------------
// Message CRUD
// ------------------------------------------------------------------

// AppendMessage adds a new message to a session, auto-incrementing sequence.
func (s *Store) AppendMessage(ctx context.Context, msg *MessageDoc) (*mongo.InsertOneResult, error) {
	if msg.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if msg.MessageID == "" {
		return nil, fmt.Errorf("message_id is required")
	}

	now := time.Now().UTC()
	msg.CreatedAt = now

	// Auto-increment sequence per session using an atomic counter.
	seq, err := s.nextSequence(ctx, msg.SessionID)
	if err != nil {
		return nil, fmt.Errorf("next sequence: %w", err)
	}
	msg.Sequence = seq

	return s.messages.InsertOne(ctx, msg)
}

// GetMessage retrieves a single message by message_id.
func (s *Store) GetMessage(ctx context.Context, messageID string) (*MessageDoc, error) {
	sr := s.messages.FindOne(ctx, bson.M{"message_id": messageID})
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var msg MessageDoc
	if err := sr.Decode(&msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// GetMessageWithDeleted retrieves a message including soft-deleted ones.
func (s *Store) GetMessageWithDeleted(ctx context.Context, messageID string) (*MessageDoc, error) {
	sr := s.messages.FindOne(ctx, bson.M{"message_id": messageID}, softdelete.WithIncludeDeleted())
	if sr.Err() != nil {
		return nil, sr.Err()
	}
	var msg MessageDoc
	if err := sr.Decode(&msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// UpdateMessage patches fields of an existing message.
func (s *Store) UpdateMessage(ctx context.Context, messageID string, updates bson.M) (*mongo.UpdateResult, error) {
	return s.messages.UpdateOne(ctx, bson.M{"message_id": messageID}, bson.M{"$set": updates})
}

// DeleteMessage soft-deletes a single message.
func (s *Store) DeleteMessage(ctx context.Context, messageID string) (*mongo.UpdateResult, error) {
	return s.messages.Delete(ctx, bson.M{"message_id": messageID})
}

// RestoreMessage un-deletes a soft-deleted message.
func (s *Store) RestoreMessage(ctx context.Context, messageID string) (*mongo.UpdateResult, error) {
	return s.messages.Restore(ctx, bson.M{"message_id": messageID})
}

// HardDeleteMessage permanently removes a message.
func (s *Store) HardDeleteMessage(ctx context.Context, messageID string) (*mongo.DeleteResult, error) {
	return s.messages.HardDelete(ctx, bson.M{"message_id": messageID})
}

// ListMessages returns all messages for a session, ordered by sequence ascending.
func (s *Store) ListMessages(ctx context.Context, sessionID string, opts ...ListMessagesOption) ([]MessageDoc, error) {
	cfg := applyListMessagesOpts(opts)

	filter := bson.M{"session_id": sessionID}
	if !cfg.IncludeDeleted {
		filter["deleted"] = bson.M{"$ne": true}
	}

	findOpts := options.Find().SetSort(bson.D{{Key: "sequence", Value: 1}})
	if cfg.Limit > 0 {
		findOpts.SetLimit(int64(cfg.Limit))
	}

	cur, err := s.messagesRaw.Find(ctx, filter, findOpts)
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)

	var msgs []MessageDoc
	if err := cur.All(ctx, &msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}

// ListMessagesOption configures the ListMessages query.
type ListMessagesOption func(*listMessagesConfig)

type listMessagesConfig struct {
	Limit          int
	IncludeDeleted bool
}

func applyListMessagesOpts(opts []ListMessagesOption) listMessagesConfig {
	var cfg listMessagesConfig
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// WithIncludeDeleted includes soft-deleted messages in ListMessages.
func WithIncludeDeleted() ListMessagesOption {
	return func(c *listMessagesConfig) {
		c.IncludeDeleted = true
	}
}

// WithLimit limits the number of messages returned.
func WithLimit(n int) ListMessagesOption {
	return func(c *listMessagesConfig) {
		c.Limit = n
	}
}

// ------------------------------------------------------------------
// Sequence counter (atomic, per-session)
// ------------------------------------------------------------------

// nextSequence returns the next sequence number for a session using FindOneAndUpdate
// with $inc on a counter document. If no counter exists, it starts at 1.
func (s *Store) nextSequence(ctx context.Context, sessionID string) (int, error) {
	counterColl := s.messagesRaw.Database().Collection("session_message_counters")
	filter := bson.M{"session_id": sessionID}
	update := bson.M{"$inc": bson.M{"sequence": 1}}
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)

	var result bson.M
	err := counterColl.FindOneAndUpdate(ctx, filter, update, opts).Decode(&result)
	if err != nil {
		return 0, err
	}

	seq, ok := result["sequence"].(int32)
	if !ok {
		// Try int64 for newer MongoDB versions
		seq64, ok64 := result["sequence"].(int64)
		if ok64 {
			return int(seq64), nil
		}
		return 0, fmt.Errorf("unexpected sequence type: %T", result["sequence"])
	}
	return int(seq), nil
}
