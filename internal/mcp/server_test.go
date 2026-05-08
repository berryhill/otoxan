package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

func TestReadFramed(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "basic",
			input: "Content-Length: 7\r\n\r\n{\"a\":1}",
			want:  `{"a":1}`,
		},
		{
			name:  "no trailing newline in body",
			input: "Content-Length: 7\r\n\r\n{\"a\":1}",
			want:  `{"a":1}`,
		},
		{
			name:    "missing content-length",
			input:   "\r\n\r\n{}",
			wantErr: true,
		},
		{
			name:    "eof before headers",
			input:   "",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReadFramed(strings.NewReader(tt.input))
			if (err != nil) != tt.wantErr {
				t.Fatalf("readFramed() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if string(got) != tt.want {
				t.Fatalf("readFramed() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWriteFramed(t *testing.T) {
	var buf bytes.Buffer
	body := []byte(`{"ok":true}`)
	if err := WriteFramed(&buf, body); err != nil {
		t.Fatal(err)
	}
	want := "Content-Length: 11\r\n\r\n{\"ok\":true}"
	if buf.String() != want {
		t.Fatalf("writeFramed() = %q, want %q", buf.String(), want)
	}
}

// safeBuffer wraps bytes.Buffer with a mutex for concurrent access.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (sb *safeBuffer) Write(p []byte) (n int, err error) {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.Write(p)
}

func (sb *safeBuffer) String() string {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.buf.String()
}

func (sb *safeBuffer) Reader() *bytes.Reader {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return bytes.NewReader(sb.buf.Bytes())
}

func TestServeInitialize(t *testing.T) {
	s := New("test-srv", "1.0.0")

	pr, pw := io.Pipe()
	var out safeBuffer

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Serve(ctx, pr, &out)
	}()

	// Write request.
	req := encodeReq(request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if _, err := pw.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	pw.Close()

	// Wait for Serve to finish.
	<-done

	// Read response.
	resp, err := readFramed(bufio.NewReader(out.Reader()))
	if err != nil {
		t.Fatal(err)
	}
	var r Response
	if err := json.Unmarshal(resp, &r); err != nil {
		t.Fatal(err)
	}
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}
	m, ok := r.Result.(map[string]any)
	if !ok {
		t.Fatal("result not a map")
	}
	info, ok := m["serverInfo"].(map[string]any)
	if !ok {
		t.Fatal("serverInfo not a map")
	}
	if info["name"] != "test-srv" {
		t.Fatalf("name = %v, want test-srv", info["name"])
	}
}

func TestServeToolsList(t *testing.T) {
	s := New("test", "0.1")
	s.Register(Tool{
		Name:        "echo",
		Description: "echoes back",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Handler: func(_ context.Context, raw json.RawMessage) (any, error) {
			return string(raw), nil
		},
	})

	pr, pw := io.Pipe()
	var out safeBuffer

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Serve(ctx, pr, &out)
	}()

	req := encodeReq(request{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
	})
	if _, err := pw.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	pw.Close()

	<-done

	resp, err := readFramed(bufio.NewReader(out.Reader()))
	if err != nil {
		t.Fatal(err)
	}
	var r Response
	if err := json.Unmarshal(resp, &r); err != nil {
		t.Fatal(err)
	}
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}
}

func TestServeToolsCall(t *testing.T) {
	s := New("test", "0.1")
	s.Register(Tool{
		Name:        "add",
		Description: "adds two ints",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"integer"}}}`),
		Handler: func(_ context.Context, raw json.RawMessage) (any, error) {
			var args struct{ A, B int }
			if err := json.Unmarshal(raw, &args); err != nil {
				return nil, err
			}
			return args.A + args.B, nil
		},
	})

	pr, pw := io.Pipe()
	var out safeBuffer

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Serve(ctx, pr, &out)
	}()

	req := encodeReq(request{
		JSONRPC: "2.0",
		ID:      42,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"add","arguments":{"a":2,"b":3}}`),
	})
	if _, err := pw.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	pw.Close()

	<-done

	resp, err := readFramed(bufio.NewReader(out.Reader()))
	if err != nil {
		t.Fatal(err)
	}
	var r Response
	if err := json.Unmarshal(resp, &r); err != nil {
		t.Fatal(err)
	}
	if r.Error != nil {
		t.Fatalf("unexpected error: %v", r.Error)
	}
	if r.ID != float64(42) {
		t.Fatalf("id = %v, want 42", r.ID)
	}
}

func TestServeMethodNotFound(t *testing.T) {
	s := New("test", "0.1")

	pr, pw := io.Pipe()
	var out safeBuffer

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Serve(ctx, pr, &out)
	}()

	req := encodeReq(request{
		JSONRPC: "2.0",
		ID:      7,
		Method:  "foo/bar",
	})
	if _, err := pw.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	pw.Close()

	<-done

	resp, err := readFramed(bufio.NewReader(out.Reader()))
	if err != nil {
		t.Fatal(err)
	}
	var r Response
	if err := json.Unmarshal(resp, &r); err != nil {
		t.Fatal(err)
	}
	if r.Error == nil {
		t.Fatal("expected error")
	}
	if r.Error.Code != CodeMethodNotFound {
		t.Fatalf("code = %d, want %d", r.Error.Code, CodeMethodNotFound)
	}
}

func TestServePanicRecovery(t *testing.T) {
	s := New("test", "0.1")
	s.Register(Tool{
		Name:        "panic",
		Description: "always panics",
		InputSchema: json.RawMessage(`{}`),
		Handler: func(_ context.Context, _ json.RawMessage) (any, error) {
			panic("boom")
		},
	})

	pr, pw := io.Pipe()
	var out safeBuffer

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Serve(ctx, pr, &out)
	}()

	req := encodeReq(request{
		JSONRPC: "2.0",
		ID:      99,
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"panic","arguments":{}}`),
	})
	if _, err := pw.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	pw.Close()

	<-done

	resp, err := readFramed(bufio.NewReader(out.Reader()))
	if err != nil {
		t.Fatal(err)
	}
	var r Response
	if err := json.Unmarshal(resp, &r); err != nil {
		t.Fatal(err)
	}
	if r.Error == nil {
		t.Fatal("expected error")
	}
	if r.Error.Code != CodeInternalError {
		t.Fatalf("code = %d, want %d", r.Error.Code, CodeInternalError)
	}
}

func TestServeContextCancel(t *testing.T) {
	s := New("test", "0.1")
	in := strings.NewReader("") // empty so Serve blocks on read
	var out bytes.Buffer

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Serve(ctx, in, &out)
	}()

	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func encodeReq(req request) string {
	b, _ := json.Marshal(req)
	var buf bytes.Buffer
	_ = WriteFramed(&buf, b)
	return buf.String()
}
