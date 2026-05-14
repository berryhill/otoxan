package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"sync"
	"time"
)

// maxMessageLen is the maximum allowed native-messaging payload in bytes.
// Chrome caps at 1 MB; we match that.
const maxMessageLen = 1024 * 1024

// message is the envelope Chrome sends / receives.
type message struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// runNativeHostLoop reads length-prefixed JSON from in and writes replies to out.
// It returns when in reaches EOF or an unrecoverable I/O error occurs.
func runNativeHostLoop(in io.Reader, out io.Writer) error {
	sw := &syncWriter{w: out}
	for {
		msg, err := readMessage(in)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			payload, _ := json.Marshal(map[string]any{"error": err.Error()})
			_ = writeMessage(sw, message{Type: "error", Data: payload})
			return err
		}
		if msg == nil {
			return nil
		}

		reply, err := handleMessage(msg)
		if err != nil {
			payload, _ := json.Marshal(map[string]any{"error": err.Error()})
			reply = message{Type: "error", Data: payload}
		}

		if err := writeMessage(sw, reply); err != nil {
			return err
		}
	}
}

// syncWriter wraps an io.Writer with a sync.Mutex so concurrent writes are safe.
type syncWriter struct {
	w  io.Writer
	mu sync.Mutex
}

func (sw *syncWriter) Write(p []byte) (n int, err error) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	return sw.w.Write(p)
}

// handleMessage routes inbound messages to their handlers.
func handleMessage(msg *message) (message, error) {
	switch msg.Type {
	case "hello":
		payload, _ := json.Marshal(map[string]string{"version": "0.1.0", "go_version": runtime.Version(), "daemon_name": "otoxan-companion"})
		return message{Type: "welcome", Data: payload}, nil
	case "start_session":
		payload, _ := json.Marshal(map[string]any{"ok": true, "session_id": "demo-session-" + fmt.Sprintf("%d", time.Now().UnixNano())})
		return message{Type: "start_session_reply", Data: payload}, nil
	case "send_message":
		payload, _ := json.Marshal(map[string]any{"ok": true})
		return message{Type: "done", Data: payload}, nil
	default:
		return message{}, fmt.Errorf("unknown message type: %q", msg.Type)
	}
}

// readMessage reads a 32-bit little-endian length-prefixed JSON message.
func readMessage(r io.Reader) (*message, error) {
	var length uint32
	if err := binary.Read(r, binary.LittleEndian, &length); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("read length: %w", err)
	}
	if length > maxMessageLen {
		return nil, fmt.Errorf("message length %d exceeds max %d", length, maxMessageLen)
	}
	if length == 0 {
		return nil, fmt.Errorf("empty message")
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, fmt.Errorf("short read: expected %d bytes, got %v", length, err)
		}
		return nil, fmt.Errorf("read payload: %w", err)
	}

	var msg message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("malformed JSON: %w", err)
	}
	return &msg, nil
}

// writeMessage writes a 32-bit little-endian length-prefixed JSON message.
func writeMessage(w io.Writer, msg message) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	if len(payload) > maxMessageLen {
		return fmt.Errorf("reply length %d exceeds max %d", len(payload), maxMessageLen)
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(len(payload))); err != nil {
		return fmt.Errorf("write length: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("write payload: %w", err)
	}
	return nil
}
