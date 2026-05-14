// Package channels provides concrete ChannelAdapter implementations for the
// otoxan dispatch lane.
package channels

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// ------------------------------------------------------------------
// WhatsmeowBridge — JSON-line Unix socket client
// ------------------------------------------------------------------

// WhatsmeowBridge connects to the otoxan-whatsapp-bridge Unix socket and
// exchanges JSON-line commands.
type WhatsmeowBridge struct {
	socketPath string
	dialTimeout time.Duration
}

// NewWhatsmeowBridge creates a bridge client using the default socket path.
func NewWhatsmeowBridge() *WhatsmeowBridge {
	home := os.Getenv("OTOXAN_HOME")
	if home == "" {
		home = filepath.Join(os.Getenv("HOME"), ".local", "share", "otoxan")
	}
	return &WhatsmeowBridge{
		socketPath:  filepath.Join(home, "whatsapp-bridge.sock"),
		dialTimeout: 5 * time.Second,
	}
}

// WithSocketPath overrides the default Unix socket path.
func (w *WhatsmeowBridge) WithSocketPath(p string) *WhatsmeowBridge {
	w.socketPath = p
	return w
}

// SendResult is the reply from a successful send command.
type SendResult struct {
	Type      string `json:"type"`
	ID        string `json:"id,omitempty"`
	MessageID string `json:"messageId,omitempty"`
}

// Send sends a text message (optionally with attachments) to the given JID or
// phone number via the bridge.
func (w *WhatsmeowBridge) Send(ctx context.Context, req SendRequest) (*SendResult, error) {
	conn, err := net.DialTimeout("unix", w.socketPath, w.dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("dial bridge: %w", err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return nil, fmt.Errorf("set deadline: %w", err)
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	payload = append(payload, '\n')

	if _, err := conn.Write(payload); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	// Read reply line
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("read reply: %w", err)
		}
		return nil, fmt.Errorf("no reply from bridge")
	}

	var reply SendResult
	if err := json.Unmarshal(scanner.Bytes(), &reply); err != nil {
		return nil, fmt.Errorf("unmarshal reply: %w", err)
	}

	if reply.Type == "error" {
		return nil, fmt.Errorf("bridge error: %s", reply.ID)
	}
	return &reply, nil
}

// StatusResult is the reply from a status command.
type StatusResult struct {
	Type      string `json:"type"`
	Connected bool   `json:"connected"`
}

// Status queries the bridge for its WhatsApp connection state.
func (w *WhatsmeowBridge) Status(ctx context.Context) (*StatusResult, error) {
	conn, err := net.DialTimeout("unix", w.socketPath, w.dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("dial bridge: %w", err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return nil, fmt.Errorf("set deadline: %w", err)
	}

	payload := []byte(`{"type":"status"}
`)
	if _, err := conn.Write(payload); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("read reply: %w", err)
		}
		return nil, fmt.Errorf("no reply from bridge")
	}

	var reply StatusResult
	if err := json.Unmarshal(scanner.Bytes(), &reply); err != nil {
		return nil, fmt.Errorf("unmarshal reply: %w", err)
	}
	return &reply, nil
}

// ------------------------------------------------------------------
// SendRequest — JSON-line payload accepted by the bridge
// ------------------------------------------------------------------

// SendRequest is the JSON-line payload sent to the bridge socket.
type SendRequest struct {
	Type     string          `json:"type"`              // always "send"
	ID       string          `json:"id,omitempty"`      // caller correlation id
	JID      string          `json:"jid,omitempty"`   // WhatsApp JID (e.g. 1234567890@s.whatsapp.net)
	Phone    string          `json:"phone,omitempty"` // E.164 without +; bridge builds JID
	Text     string          `json:"text,omitempty"`
	Document *DocumentAttachment `json:"document,omitempty"`
	Image    *ImageAttachment    `json:"image,omitempty"`
}

// DocumentAttachment carries a file to send as a document.
type DocumentAttachment struct {
	Path     string `json:"path"`
	MimeType string `json:"mimetype,omitempty"`
	FileName string `json:"fileName,omitempty"`
}

// ImageAttachment carries a file to send as an image.
type ImageAttachment struct {
	Path string `json:"path"`
}

// ------------------------------------------------------------------
// ChannelAdapter interface conformance (placeholder)
// ------------------------------------------------------------------

// ChannelAdapter is the interface the dispatch lane expects.
// It lives here as documentation until the dispatch lane imports it.
type ChannelAdapter interface {
	Send(ctx context.Context, req SendRequest) (*SendResult, error)
	Status(ctx context.Context) (*StatusResult, error)
}

// Compile-time check: WhatsmeowBridge implements ChannelAdapter.
var _ ChannelAdapter = (*WhatsmeowBridge)(nil)
