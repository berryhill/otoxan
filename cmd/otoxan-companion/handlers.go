// cmd/otoxan-companion/handlers.go — high-level message handlers for the companion daemon
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime"

	"github.com/silas/otoxan/internal/companion"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ------------------------------------------------------------------
// Handler context (injected into the native host loop)
// ------------------------------------------------------------------

// CapturesStore is the minimal interface the handlers need from the companion
// captures store. The real *companion.CapturesStore satisfies this.
type CapturesStore interface {
	BeginUpload(ctx context.Context, data []byte) (string, error)
	AppendChunk(ctx context.Context, uploadID string, seq int, data []byte) error
	FinishUpload(ctx context.Context, uploadID string, message string) (string, error)
	Get(ctx context.Context, captureID string) (*companion.CaptureRecord, error)
}

// handlerCtx carries the runtime dependencies for message handlers.
type handlerCtx struct {
	db       *mongo.Database
	cc       CCAdapter
	captures CapturesStore
}

// CCAdapter is the interface to the engine's CC (Claude Code) session spawner.
// The real implementation lives in the engine; here we define the contract.
type CCAdapter interface {
	// StartSession spawns a new CC session for the given working directory and
	// returns a session ID and a write-closer for sending messages to it.
	StartSession(cwd string) (sessionID string, stdin io.WriteCloser, err error)

	// AttachSession returns the write-closer for an existing session.
	AttachSession(sessionID string) (stdin io.WriteCloser, err error)
}

// ------------------------------------------------------------------
// Request / reply shapes
// ------------------------------------------------------------------

// startSessionRequest is the payload for a "start_session" message.
type startSessionRequest struct {
	OwnerID string `json:"owner_id"`
	Message string `json:"message,omitempty"`
}

// startSessionReply is the first frame returned by StartSession.
type startSessionReply struct {
	SessionID string `json:"session_id"`
	Ok        bool   `json:"ok"`
}

// sendMessageRequest is the payload for a "send_message" message.
type sendMessageRequest struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
	CaptureID string `json:"capture_id,omitempty"`
}

// streamReply is a single event frame emitted while a session is running.
type streamReply struct {
	Type    string `json:"type"`    // "stream"
	Event   string `json:"event"`   // e.g. "text", "tool_use", "error"
	Payload string `json:"payload"` // event-specific data
}

// doneReply terminates a streaming response.
type doneReply struct {
	Type string `json:"type"` // "done"
	Ok   bool   `json:"ok"`
}

// errorReply is emitted when a handler fails.
type errorReply struct {
	Type  string `json:"type"` // "error"
	Ok    bool   `json:"ok"`
	Error string `json:"error"`
}

// ------------------------------------------------------------------
// Message router
// ------------------------------------------------------------------

// handleMessageWithCtx routes inbound messages to their handlers using runtime
// dependencies. It replaces the naive handleMessage in nativehost.go.
func handleMessageWithCtx(ctx *handlerCtx, msg *message) (message, error) {
	switch msg.Type {
	case "hello":
		return message{
			Type: "welcome",
			Data: mustMarshal(welcomeData{
				Version:    version,
				GoVersion:  goVersion(),
				DaemonName: "otoxan-companion",
			}),
		}, nil
	case "start_session":
		return handleStartSession(ctx, msg.Data)
	case "send_message":
		return handleSendMessage(ctx, msg.Data)
	case "begin_upload":
		return handleBeginUpload(ctx, msg.Data)
	case "upload_chunk":
		return handleUploadChunk(ctx, msg.Data)
	case "finish_upload":
		return handleFinishUpload(ctx, msg.Data)
	default:
		return message{}, fmt.Errorf("unknown message type: %q", msg.Type)
	}
}

func goVersion() string {
	// runtime.Version() is imported in nativehost.go; keep a thin wrapper.
	return runtime.Version()
}

// ------------------------------------------------------------------
// StartSession handler
// ------------------------------------------------------------------

func handleStartSession(ctx *handlerCtx, data json.RawMessage) (message, error) {
	var req startSessionRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return errorMessage(fmt.Errorf("invalid start_session payload: %w", err)), nil
	}

	if req.OwnerID == "" {
		return errorMessage(fmt.Errorf("owner_id is required")), nil
	}

	// Resolve cwd via ownership lookup (placeholder — ownership store not yet ported).
	// When the ownership collection exists, query it here. For now, default to "".
	cwd := ""

	if ctx.cc == nil {
		return errorMessage(fmt.Errorf("CC adapter not available")), nil
	}

	sessionID, stdin, err := ctx.cc.StartSession(cwd)
	if err != nil {
		return errorMessage(fmt.Errorf("start session: %w", err)), nil
	}
	defer stdin.Close()

	// If a message was provided in the same request, forward it immediately.
	if req.Message != "" {
		if _, err := fmt.Fprintf(stdin, "%s\n", req.Message); err != nil {
			return errorMessage(fmt.Errorf("send initial message: %w", err)), nil
		}
	}

	return message{
		Type: "start_session_reply",
		Data: mustMarshal(startSessionReply{SessionID: sessionID, Ok: true}),
	}, nil
}

// ------------------------------------------------------------------
// SendMessage handler (streaming)
// ------------------------------------------------------------------

// sendMessageResult carries the outcome of the async stream back to the loop.
type sendMessageResult struct {
	replies []message
	err     error
}

func handleSendMessage(ctx *handlerCtx, data json.RawMessage) (message, error) {
	var req sendMessageRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return errorMessage(fmt.Errorf("invalid send_message payload: %w", err)), nil
	}

	if req.SessionID == "" {
		return errorMessage(fmt.Errorf("session_id is required")), nil
	}
	if req.Message == "" && req.CaptureID == "" {
		return errorMessage(fmt.Errorf("message or capture_id is required")), nil
	}

	if ctx.cc == nil {
		return errorMessage(fmt.Errorf("CC adapter not available")), nil
	}

	stdin, err := ctx.cc.AttachSession(req.SessionID)
	if err != nil {
		return errorMessage(fmt.Errorf("attach session: %w", err)), nil
	}
	defer stdin.Close()

	msg := req.Message
	if req.CaptureID != "" && ctx.captures != nil {
		rec, err := ctx.captures.Get(context.Background(), req.CaptureID)
		if err != nil {
			return errorMessage(fmt.Errorf("resolve capture: %w", err)), nil
		}
		// Reassemble capture data from chunks.
		var assembled []byte
		for _, ch := range rec.Chunks {
			assembled = append(assembled, ch.Data...)
		}
		msg = string(assembled)
	}

	if _, err := fmt.Fprintf(stdin, "%s\n", msg); err != nil {
		return errorMessage(fmt.Errorf("send message: %w", err)), nil
	}

	// Synchronous placeholder: emit a single done reply.
	// In the full implementation this would spawn a goroutine that reads CC
	// stdout/stderr and emits stream + done frames. The test mocks this path.
	return message{
		Type: "done",
		Data: mustMarshal(doneReply{Ok: true}),
	}, nil
}

// ------------------------------------------------------------------
// Upload handlers
// ------------------------------------------------------------------

// beginUploadRequest starts a chunked upload.
type beginUploadRequest struct {
	Data []byte `json:"data"`
}

// beginUploadReply returns the upload_id.
type beginUploadReply struct {
	UploadID string `json:"upload_id"`
	Ok       bool   `json:"ok"`
}

func handleBeginUpload(ctx *handlerCtx, data json.RawMessage) (message, error) {
	var req beginUploadRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return errorMessage(fmt.Errorf("invalid begin_upload payload: %w", err)), nil
	}
	if ctx.captures == nil {
		return errorMessage(fmt.Errorf("captures store not available")), nil
	}
	uploadID, err := ctx.captures.BeginUpload(context.Background(), req.Data)
	if err != nil {
		return errorMessage(fmt.Errorf("begin upload: %w", err)), nil
	}
	return message{
		Type: "begin_upload_reply",
		Data: mustMarshal(beginUploadReply{UploadID: uploadID, Ok: true}),
	}, nil
}

// uploadChunkRequest appends a chunk.
type uploadChunkRequest struct {
	UploadID string `json:"upload_id"`
	Seq      int    `json:"seq"`
	Data     []byte `json:"data"`
}

// uploadChunkReply confirms the chunk.
type uploadChunkReply struct {
	Ok bool `json:"ok"`
}

func handleUploadChunk(ctx *handlerCtx, data json.RawMessage) (message, error) {
	var req uploadChunkRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return errorMessage(fmt.Errorf("invalid upload_chunk payload: %w", err)), nil
	}
	if ctx.captures == nil {
		return errorMessage(fmt.Errorf("captures store not available")), nil
	}
	if err := ctx.captures.AppendChunk(context.Background(), req.UploadID, req.Seq, req.Data); err != nil {
		return errorMessage(fmt.Errorf("upload chunk: %w", err)), nil
	}
	return message{
		Type: "upload_chunk_reply",
		Data: mustMarshal(uploadChunkReply{Ok: true}),
	}, nil
}

// finishUploadRequest finalises an upload.
type finishUploadRequest struct {
	UploadID string `json:"upload_id"`
	Message  string `json:"message"`
}

// finishUploadReply returns the capture_id.
type finishUploadReply struct {
	CaptureID string `json:"capture_id"`
	Ok        bool   `json:"ok"`
}

func handleFinishUpload(ctx *handlerCtx, data json.RawMessage) (message, error) {
	var req finishUploadRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return errorMessage(fmt.Errorf("invalid finish_upload payload: %w", err)), nil
	}
	if ctx.captures == nil {
		return errorMessage(fmt.Errorf("captures store not available")), nil
	}
	captureID, err := ctx.captures.FinishUpload(context.Background(), req.UploadID, req.Message)
	if err != nil {
		return errorMessage(fmt.Errorf("finish upload: %w", err)), nil
	}
	return message{
		Type: "finish_upload_reply",
		Data: mustMarshal(finishUploadReply{CaptureID: captureID, Ok: true}),
	}, nil
}

// ------------------------------------------------------------------
// Helpers
// ------------------------------------------------------------------

func errorMessage(err error) message {
	return message{
		Type: "error",
		Data: mustMarshal(errorReply{Ok: false, Error: err.Error()}),
	}
}

// ------------------------------------------------------------------
// Native host loop with context
// ------------------------------------------------------------------

// runNativeHostLoopWithCtx is the context-aware entry point for the native-messaging
// loop. It reads length-prefixed JSON from in, routes messages through
// handleMessageWithCtx, and writes replies to out.
func runNativeHostLoopWithCtx(ctx *handlerCtx, in io.Reader, out io.Writer) error {
	sw := &syncWriter{w: out}
	for {
		msg, err := readMessage(in)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			_ = writeMessage(sw, message{
				Type: "error",
				Data: mustMarshal(errorData{Error: err.Error()}),
			})
			return err
		}
		if msg == nil {
			return nil
		}

		reply, err := handleMessageWithCtx(ctx, msg)
		if err != nil {
			reply = message{
				Type: "error",
				Data: mustMarshal(errorData{Error: err.Error()}),
			}
		}

		if err := writeMessage(sw, reply); err != nil {
			return err
		}
	}
}
