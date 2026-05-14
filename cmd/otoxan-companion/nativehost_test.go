package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ------------------------------------------------------------------
// Helpers
// ------------------------------------------------------------------

// encodeFrame builds a 4-byte LE length prefix + JSON payload.
func encodeFrameNative(t *testing.T, v any) []byte {
	t.Helper()
	payload, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal frame payload: %v", err)
	}
	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.LittleEndian, uint32(len(payload)))
	buf.Write(payload)
	return buf.Bytes()
}

// encodeRawFrame builds a frame from raw payload bytes.
func encodeRawFrame(t *testing.T, payload []byte) []byte {
	t.Helper()
	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.LittleEndian, uint32(len(payload)))
	buf.Write(payload)
	return buf.Bytes()
}

// ------------------------------------------------------------------
// Happy path: small message round-trip
// ------------------------------------------------------------------

func TestNativeHost_HelloRoundTrip(t *testing.T) {
	pr, pw := io.Pipe()
	outBuf := new(bytes.Buffer)

	go func() {
		_ = runNativeHostLoop(pr, outBuf)
	}()

	pw.Write(encodeFrameNative(t, map[string]any{"type": "hello"}))
	pw.Close()

	// Give the goroutine a moment to write before we start reading.
	time.Sleep(10 * time.Millisecond)

	// Read the reply from outBuf.
	var length uint32
	if err := binary.Read(outBuf, binary.LittleEndian, &length); err != nil {
		t.Fatalf("read reply length: %v", err)
	}
	reply := make([]byte, length)
	if _, err := io.ReadFull(outBuf, reply); err != nil {
		t.Fatalf("read reply payload: %v", err)
	}

	var msg message
	if err := json.Unmarshal(reply, &msg); err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	if msg.Type != "welcome" {
		t.Fatalf("expected type welcome, got %q", msg.Type)
	}

	var data welcomeData
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		t.Fatalf("unmarshal welcome data: %v", err)
	}
	if data.Version != version {
		t.Errorf("version mismatch: got %q, want %q", data.Version, version)
	}
	if data.GoVersion != runtime.Version() {
		t.Errorf("go_version mismatch: got %q, want %q", data.GoVersion, runtime.Version())
	}
	if data.DaemonName != "otoxan-companion" {
		t.Errorf("daemon_name mismatch: got %q", data.DaemonName)
	}
}

// ------------------------------------------------------------------
// 64 KB boundary
// ------------------------------------------------------------------

func TestNativeHost_LargePayload(t *testing.T) {
	pr, pw := io.Pipe()
	outBuf := new(bytes.Buffer)

	go func() {
		_ = runNativeHostLoop(pr, outBuf)
	}()

	// Build a 64 KB payload (just under the 1 MB cap).
	big := strings.Repeat("x", 64*1024)
	pw.Write(encodeFrameNative(t, map[string]any{"type": "hello", "data": big}))
	pw.Close()

	time.Sleep(10 * time.Millisecond)

	var length uint32
	if err := binary.Read(outBuf, binary.LittleEndian, &length); err != nil {
		t.Fatalf("read reply length: %v", err)
	}
	reply := make([]byte, length)
	if _, err := io.ReadFull(outBuf, reply); err != nil {
		t.Fatalf("read reply payload: %v", err)
	}

	var msg message
	if err := json.Unmarshal(reply, &msg); err != nil {
		t.Fatalf("unmarshal reply: %v", err)
	}
	if msg.Type != "welcome" {
		t.Fatalf("expected welcome, got %q", msg.Type)
	}
}

// ------------------------------------------------------------------
// Malformed length (exceeds max)
// ------------------------------------------------------------------

func TestNativeHost_LengthExceedsMax(t *testing.T) {
	pr, pw := io.Pipe()
	outBuf := new(bytes.Buffer)

	go func() {
		_ = runNativeHostLoop(pr, outBuf)
	}()

	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.LittleEndian, uint32(maxMessageLen+1))
	pw.Write(buf.Bytes())
	pw.Close()

	time.Sleep(10 * time.Millisecond)

	// The loop should send an error reply and then exit.
	var length uint32
	if err := binary.Read(outBuf, binary.LittleEndian, &length); err != nil {
		t.Fatalf("read error reply length: %v", err)
	}
	reply := make([]byte, length)
	if _, err := io.ReadFull(outBuf, reply); err != nil {
		t.Fatalf("read error reply payload: %v", err)
	}

	var msg message
	if err := json.Unmarshal(reply, &msg); err != nil {
		t.Fatalf("unmarshal error reply: %v", err)
	}
	if msg.Type != "error" {
		t.Fatalf("expected error type, got %q", msg.Type)
	}
	var data map[string]any
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		t.Fatalf("unmarshal error data: %v", err)
	}
	if !strings.Contains(data["error"].(string), "exceeds max") {
		t.Fatalf("expected 'exceeds max' in error, got %q", data["error"])
	}
}

// ------------------------------------------------------------------
// Malformed JSON
// ------------------------------------------------------------------

func TestNativeHost_MalformedJSON(t *testing.T) {
	pr, pw := io.Pipe()
	outBuf := new(bytes.Buffer)

	go func() {
		_ = runNativeHostLoop(pr, outBuf)
	}()

	pw.Write(encodeRawFrame(t, []byte("{not json")))
	pw.Close()

	time.Sleep(10 * time.Millisecond)

	var length uint32
	if err := binary.Read(outBuf, binary.LittleEndian, &length); err != nil {
		t.Fatalf("read error reply length: %v", err)
	}
	reply := make([]byte, length)
	if _, err := io.ReadFull(outBuf, reply); err != nil {
		t.Fatalf("read error reply payload: %v", err)
	}

	var msg message
	if err := json.Unmarshal(reply, &msg); err != nil {
		t.Fatalf("unmarshal error reply: %v", err)
	}
	if msg.Type != "error" {
		t.Fatalf("expected error type, got %q", msg.Type)
	}
	var data map[string]any
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		t.Fatalf("unmarshal error data: %v", err)
	}
	if !strings.Contains(data["error"].(string), "malformed JSON") {
		t.Fatalf("expected 'malformed JSON' in error, got %q", data["error"])
	}
}

// ------------------------------------------------------------------
// Unknown message type
// ------------------------------------------------------------------

func TestNativeHost_UnknownType(t *testing.T) {
	pr, pw := io.Pipe()
	outBuf := new(bytes.Buffer)

	go func() {
		_ = runNativeHostLoop(pr, outBuf)
	}()

	pw.Write(encodeFrameNative(t, map[string]any{"type": "nope"}))
	pw.Close()

	time.Sleep(10 * time.Millisecond)

	var length uint32
	if err := binary.Read(outBuf, binary.LittleEndian, &length); err != nil {
		t.Fatalf("read error reply length: %v", err)
	}
	reply := make([]byte, length)
	if _, err := io.ReadFull(outBuf, reply); err != nil {
		t.Fatalf("read error reply payload: %v", err)
	}

	var msg message
	if err := json.Unmarshal(reply, &msg); err != nil {
		t.Fatalf("unmarshal error reply: %v", err)
	}
	if msg.Type != "error" {
		t.Fatalf("expected error type, got %q", msg.Type)
	}
	var data map[string]any
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		t.Fatalf("unmarshal error data: %v", err)
	}
	if !strings.Contains(data["error"].(string), "unknown message type") {
		t.Fatalf("expected 'unknown message type' in error, got %q", data["error"])
	}
}

// ------------------------------------------------------------------
// Clean EOF with no frames
// ------------------------------------------------------------------

func TestNativeHost_EmptyEOF(t *testing.T) {
	pr, pw := io.Pipe()
	outBuf := new(bytes.Buffer)

	done := make(chan error, 1)
	go func() {
		done <- runNativeHostLoop(pr, outBuf)
	}()

	pw.Close()

	if err := <-done; err != nil {
		t.Fatalf("expected nil error on clean EOF, got %v", err)
	}
	if outBuf.Len() != 0 {
		t.Fatalf("expected no output on clean EOF, got %d bytes", outBuf.Len())
	}
}

// ------------------------------------------------------------------
// Short read (payload shorter than declared length)
// ------------------------------------------------------------------

func TestNativeHost_ShortRead(t *testing.T) {
	pr, pw := io.Pipe()
	outBuf := new(bytes.Buffer)

	go func() {
		_ = runNativeHostLoop(pr, outBuf)
	}()

	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.LittleEndian, uint32(100))
	buf.WriteString("short") // only 5 bytes, not 100
	pw.Write(buf.Bytes())
	pw.Close()

	time.Sleep(10 * time.Millisecond)

	var length uint32
	if err := binary.Read(outBuf, binary.LittleEndian, &length); err != nil {
		t.Fatalf("read error reply length: %v", err)
	}
	reply := make([]byte, length)
	if _, err := io.ReadFull(outBuf, reply); err != nil {
		t.Fatalf("read error reply payload: %v", err)
	}

	var msg message
	if err := json.Unmarshal(reply, &msg); err != nil {
		t.Fatalf("unmarshal error reply: %v", err)
	}
	if msg.Type != "error" {
		t.Fatalf("expected error type, got %q", msg.Type)
	}
	var data map[string]any
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		t.Fatalf("unmarshal error data: %v", err)
	}
	if !strings.Contains(data["error"].(string), "short read") {
		t.Fatalf("expected 'short read' in error, got %q", data["error"])
	}
}

// ------------------------------------------------------------------
// Empty message (length == 0)
// ------------------------------------------------------------------

func TestNativeHost_EmptyMessage(t *testing.T) {
	pr, pw := io.Pipe()
	outBuf := new(bytes.Buffer)

	go func() {
		_ = runNativeHostLoop(pr, outBuf)
	}()

	buf := new(bytes.Buffer)
	_ = binary.Write(buf, binary.LittleEndian, uint32(0))
	pw.Write(buf.Bytes())
	pw.Close()

	time.Sleep(10 * time.Millisecond)

	var length uint32
	if err := binary.Read(outBuf, binary.LittleEndian, &length); err != nil {
		t.Fatalf("read error reply length: %v", err)
	}
	reply := make([]byte, length)
	if _, err := io.ReadFull(outBuf, reply); err != nil {
		t.Fatalf("read error reply payload: %v", err)
	}

	var msg message
	if err := json.Unmarshal(reply, &msg); err != nil {
		t.Fatalf("unmarshal error reply: %v", err)
	}
	if msg.Type != "error" {
		t.Fatalf("expected error type, got %q", msg.Type)
	}
	var data map[string]any
	if err := json.Unmarshal(msg.Data, &data); err != nil {
		t.Fatalf("unmarshal error data: %v", err)
	}
	if !strings.Contains(data["error"].(string), "empty message") {
		t.Fatalf("expected 'empty message' in error, got %q", data["error"])
	}
}

// ------------------------------------------------------------------
// Two sequential messages in one stream
// ------------------------------------------------------------------

func TestNativeHost_TwoMessages(t *testing.T) {
	pr, pw := io.Pipe()
	outBuf := new(bytes.Buffer)

	go func() {
		_ = runNativeHostLoop(pr, outBuf)
	}()

	pw.Write(encodeFrameNative(t, map[string]any{"type": "hello"}))
	pw.Write(encodeFrameNative(t, map[string]any{"type": "hello"}))
	pw.Close()

	time.Sleep(10 * time.Millisecond)

	for i := 0; i < 2; i++ {
		var length uint32
		if err := binary.Read(outBuf, binary.LittleEndian, &length); err != nil {
			t.Fatalf("read reply %d length: %v", i, err)
		}
		reply := make([]byte, length)
		if _, err := io.ReadFull(outBuf, reply); err != nil {
			t.Fatalf("read reply %d payload: %v", i, err)
		}
		var msg message
		if err := json.Unmarshal(reply, &msg); err != nil {
			t.Fatalf("unmarshal reply %d: %v", i, err)
		}
		if msg.Type != "welcome" {
			t.Fatalf("reply %d: expected welcome, got %q", i, msg.Type)
		}
	}
}

// ------------------------------------------------------------------
// io.Pipe close-with-error should propagate
// ------------------------------------------------------------------

func TestNativeHost_ReadError(t *testing.T) {
	pr, pw := io.Pipe()
	outBuf := new(bytes.Buffer)

	done := make(chan error, 1)
	go func() {
		done <- runNativeHostLoop(pr, outBuf)
	}()

	pw.CloseWithError(errors.New("boom"))

	err := <-done
	if err == nil {
		t.Fatal("expected error from broken pipe, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected error to contain 'boom', got %v", err)
	}
}
