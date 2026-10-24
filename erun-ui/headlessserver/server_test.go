package headlessserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

type stubTarget struct {
	calls []string
}

func (s *stubTarget) Echo(value string) (string, error) {
	s.calls = append(s.calls, "Echo:"+value)
	return value, nil
}

func (s *stubTarget) Boom() (string, error) {
	return "", errBoom
}

func (s *stubTarget) Plain(n int) int {
	return n * 2
}

func (s *stubTarget) Void() {
	s.calls = append(s.calls, "Void")
}

type stubError struct{ msg string }

func (e *stubError) Error() string { return e.msg }

var errBoom error = &stubError{msg: "boom"}

func newTestServer(t *testing.T) (*Server, *stubTarget, *httptest.Server) {
	t.Helper()
	target := &stubTarget{}
	bundle := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><html><head><title>x</title></head><body></body></html>")},
		"assets/main.js": &fstest.MapFile{
			Data: []byte("console.log('main');"),
		},
	}
	srv := New(target, bundle)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
	})
	return srv, target, ts
}

func TestIndexInjectsShim(t *testing.T) {
	_, _, ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "window.go.main.App[\"Echo\"]") {
		t.Fatalf("shim missing Echo binding: %s", body)
	}
	if !strings.Contains(string(body), "window.runtime") {
		t.Fatalf("shim missing window.runtime: %s", body)
	}
	if !strings.Contains(string(body), "<title>x</title>") {
		t.Fatalf("original index html lost: %s", body)
	}
}

func TestStaticAssetServed(t *testing.T) {
	_, _, ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/assets/main.js")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "console.log('main')") {
		t.Fatalf("static asset body wrong: %s", body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/javascript") {
		t.Fatalf("content-type = %q", ct)
	}
}

func TestInvokeReturnsData(t *testing.T) {
	_, target, ts := newTestServer(t)
	body := mustMarshal(t, invokeRequest{Method: "Echo", Args: []json.RawMessage{json.RawMessage(`"hello"`)}})
	resp, err := http.Post(ts.URL+"/__erun_invoke", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		out, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, out)
	}
	var env invokeEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Error != "" {
		t.Fatalf("envelope error: %s", env.Error)
	}
	if env.Data != "hello" {
		t.Fatalf("data = %v, want hello", env.Data)
	}
	if len(target.calls) != 1 || target.calls[0] != "Echo:hello" {
		t.Fatalf("calls = %v", target.calls)
	}
}

func TestInvokeReturnsErrorEnvelope(t *testing.T) {
	_, _, ts := newTestServer(t)
	body := mustMarshal(t, invokeRequest{Method: "Boom", Args: []json.RawMessage{}})
	resp, err := http.Post(ts.URL+"/__erun_invoke", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	var env invokeEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Error != "boom" {
		t.Fatalf("error = %q", env.Error)
	}
}

func TestInvokeUnknownMethod(t *testing.T) {
	_, _, ts := newTestServer(t)
	body := mustMarshal(t, invokeRequest{Method: "Missing", Args: nil})
	resp, err := http.Post(ts.URL+"/__erun_invoke", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestInvokePlainReturn(t *testing.T) {
	_, _, ts := newTestServer(t)
	body := mustMarshal(t, invokeRequest{Method: "Plain", Args: []json.RawMessage{json.RawMessage(`3`)}})
	resp, err := http.Post(ts.URL+"/__erun_invoke", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var env invokeEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Data == nil {
		t.Fatalf("data missing: %+v", env)
	}
	// JSON decode gives float64 for numeric values.
	if v, ok := env.Data.(float64); !ok || int(v) != 6 {
		t.Fatalf("data = %#v", env.Data)
	}
}

func TestEmitFansOutToSubscriber(t *testing.T) {
	srv, _, ts := newTestServer(t)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/__erun_events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var wg sync.WaitGroup
	wg.Add(1)
	got := make(chan event, 1)
	go func() {
		defer wg.Done()
		readFirstSSEEvent(resp.Body, got)
	}()

	waitForSubscriber(t, srv)

	srv.Emit("ping", "hello", 42)

	select {
	case ev := <-got:
		if ev.Name != "ping" || len(ev.Args) != 2 {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event received")
	}
}

func readFirstSSEEvent(body io.Reader, got chan<- event) {
	reader := bufioReaderForSSE(body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var ev event
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &ev); err == nil {
			got <- ev
			return
		}
	}
}

func waitForSubscriber(t *testing.T, srv *Server) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for {
		srv.subsMu.Lock()
		n := len(srv.subs)
		srv.subsMu.Unlock()
		if n > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no subscriber registered")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestClipboardStoresInMemory(t *testing.T) {
	_, _, ts := newTestServer(t)
	body := mustMarshal(t, clipboardRequest{Action: "set", Text: "hello"})
	resp, err := http.Post(ts.URL+"/__erun_clipboard", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	body = mustMarshal(t, clipboardRequest{Action: "get"})
	resp, err = http.Post(ts.URL+"/__erun_clipboard", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got clipboardResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Text != "hello" {
		t.Fatalf("clipboard = %q", got.Text)
	}
}

func TestEmitReplacesDroppedEventWithGapMarker(t *testing.T) {
	srv, _, _ := newTestServer(t)

	sub := &subscriber{ch: make(chan event, 64)}
	srv.subsMu.Lock()
	srv.subs[srv.nextID.Add(1)] = sub
	srv.subsMu.Unlock()

	for i := 0; i < 64; i++ {
		sub.ch <- event{Name: "filler", Args: []any{i}}
	}

	srv.Emit("overflow")

	if got := len(sub.ch); got != 64 {
		t.Fatalf("channel length = %d, want 64 (still full, no blocking)", got)
	}

	events := drainAll(sub.ch, 64)
	assertFillerRange(t, events[:63], 1) // filler #0 was evicted to make room for the marker.
	assertGapMarker(t, events[63], 1)

	if got := sub.missed.Load(); got != 1 {
		t.Fatalf("missed = %d, want 1", got)
	}
}

func drainAll(ch <-chan event, n int) []event {
	out := make([]event, n)
	for i := range out {
		out[i] = <-ch
	}
	return out
}

func assertFillerRange(t *testing.T, events []event, startArg int) {
	t.Helper()
	for i, ev := range events {
		want := startArg + i
		got, ok := ev.Args[0].(int)
		if ev.Name != "filler" || !ok || got != want {
			t.Fatalf("event %d = %+v, want filler #%d", i, ev, want)
		}
	}
}

func assertGapMarker(t *testing.T, ev event, wantMissed int64) {
	t.Helper()
	if ev.Name != eventsDroppedName {
		t.Fatalf("event = %+v, want the %q gap marker", ev, eventsDroppedName)
	}
	if len(ev.Args) != 1 {
		t.Fatalf("gap marker args = %+v, want missed count %d", ev.Args, wantMissed)
	}
	if missed, ok := ev.Args[0].(int64); !ok || missed != wantMissed {
		t.Fatalf("gap marker args = %+v, want missed count %d", ev.Args, wantMissed)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func bufioReaderForSSE(r io.Reader) *lineReader {
	return &lineReader{r: r}
}

type lineReader struct {
	r   io.Reader
	buf []byte
}

func (lr *lineReader) ReadString(delim byte) (string, error) {
	tmp := make([]byte, 256)
	for {
		for i, b := range lr.buf {
			if b == delim {
				out := string(lr.buf[:i+1])
				lr.buf = lr.buf[i+1:]
				return out, nil
			}
		}
		n, err := lr.r.Read(tmp)
		if n > 0 {
			lr.buf = append(lr.buf, tmp[:n]...)
		}
		if err != nil {
			if len(lr.buf) > 0 {
				out := string(lr.buf)
				lr.buf = nil
				return out, nil
			}
			return "", err
		}
	}
}
