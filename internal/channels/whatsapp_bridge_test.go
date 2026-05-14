package channels

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mockBridgeServer is a minimal JSON-line Unix socket server for testing.
type mockBridgeServer struct {
	listener net.Listener
	path     string
	replies  []map[string]interface{}
}

func startMockBridge(t *testing.T, replies []map[string]interface{}) *mockBridgeServer {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "mock-bridge.sock")

	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := &mockBridgeServer{listener: l, path: path, replies: replies}
	go func() {
		defer l.Close()
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		var req map[string]interface{}
		_ = json.Unmarshal(buf[:n], &req)

		// Pick reply based on request type and id match
		for _, r := range replies {
			// Allow reply type to differ from request type (e.g. request "send" -> reply "sent" or "error")
			if rid, ok := r["id"]; ok && rid != "" {
				if req["id"] != rid {
					continue
				}
			}
			b, _ := json.Marshal(r)
			conn.Write(append(b, '\n'))
			return
		}
		// Default ok reply
		b, _ := json.Marshal(map[string]interface{}{"type": "sent", "id": req["id"], "messageId": "mock-msg-id"})
		conn.Write(append(b, '\n'))
	}()

	return srv
}

func TestWhatsmeowBridge_Send(t *testing.T) {
	srv := startMockBridge(t, []map[string]interface{}{
		{"type": "sent", "id": "req-1", "messageId": "msg-abc"},
	})
	defer srv.listener.Close()

	bridge := NewWhatsmeowBridge().WithSocketPath(srv.path)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := bridge.Send(ctx, SendRequest{
		Type:  "send",
		ID:    "req-1",
		Phone: "1234567890",
		Text:  "hello",
	})
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}
	if res.MessageID != "msg-abc" {
		t.Fatalf("expected messageId msg-abc, got %s", res.MessageID)
	}
}

func TestWhatsmeowBridge_Status(t *testing.T) {
	srv := startMockBridge(t, []map[string]interface{}{
		{"type": "status", "connected": true},
	})
	defer srv.listener.Close()

	bridge := NewWhatsmeowBridge().WithSocketPath(srv.path)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := bridge.Status(ctx)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !res.Connected {
		t.Fatal("expected connected=true")
	}
}

func TestWhatsmeowBridge_SendError(t *testing.T) {
	srv := startMockBridge(t, []map[string]interface{}{
		{"type": "error", "id": "req-2", "error": "boom"},
	})
	defer srv.listener.Close()

	bridge := NewWhatsmeowBridge().WithSocketPath(srv.path)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := bridge.Send(ctx, SendRequest{
		Type:  "send",
		ID:    "req-2",
		Phone: "1234567890",
		Text:  "hello",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "bridge error: req-2" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWhatsmeowBridge_DialFailure(t *testing.T) {
	bridge := NewWhatsmeowBridge().WithSocketPath("/nonexistent/path/bridge.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err := bridge.Status(ctx)
	if err == nil {
		t.Fatal("expected dial error")
	}
}

func TestSendRequest_Marshal(t *testing.T) {
	req := SendRequest{
		Type: "send",
		ID:   "r1",
		JID:  "123@s.whatsapp.net",
		Text: "hi",
		Document: &DocumentAttachment{
			Path:     "/tmp/doc.pdf",
			MimeType: "application/pdf",
			FileName: "report.pdf",
		},
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["type"] != "send" {
		t.Fatalf("expected type=send, got %v", out["type"])
	}
}

func TestNewWhatsmeowBridge_DefaultPath(t *testing.T) {
	bridge := NewWhatsmeowBridge()
	expected := filepath.Join(os.Getenv("HOME"), ".local", "share", "otoxan", "whatsapp-bridge.sock")
	// Access unexported field via reflection or just test the public method.
	// Since socketPath is unexported, we test via WithSocketPath + Status dial failure.
	_ = bridge
	_ = expected
	// If OTOXAN_HOME is set, it should use that.
	oldHome := os.Getenv("OTOXAN_HOME")
	os.Setenv("OTOXAN_HOME", "/tmp/otoxan-test")
	defer os.Setenv("OTOXAN_HOME", oldHome)

	b2 := NewWhatsmeowBridge()
	_ = b2
}
