// Package headlessserver hosts the ERun desktop frontend over plain HTTP so
// headless clients (Playwright, scripts, CI) can drive the same React bundle
// without a Wails window. It mirrors the runtime Wails injects into the WebView
// — a shim makes window.runtime and window.go.main.App resolve before the main
// bundle loads — so the unmodified bundle runs headless.
package headlessserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
)

// Server is the HTTP transport for headless mode. The zero value is not usable;
// construct with New.
type Server struct {
	target reflect.Value
	bundle fs.FS

	methods    map[string]reflect.Value
	methodList []string
	shimJS     string

	subsMu sync.Mutex
	subs   map[int64]chan event
	nextID atomic.Int64

	clipMu sync.Mutex
	clip   string
}

type event struct {
	Name string `json:"name"`
	Args []any  `json:"args"`
}

// New builds a Server whose target — typically *main.App — is bound by exact
// method name the same way Wails binds window.go.main.App.<Name>, so the
// headless RPC surface matches the desktop one.
func New(target any, bundle fs.FS) *Server {
	s := &Server{
		target: reflect.ValueOf(target),
		bundle: bundle,
		subs:   make(map[int64]chan event),
	}
	s.methods, s.methodList = collectExportedMethods(s.target)
	s.shimJS = buildShimJS(s.methodList)
	return s
}

// Emit fans an event out to every active SSE subscriber; the headless main
// wires it in as the App's emitter so EventsEmit-style calls reach the browser
// without Wails.
func (s *Server) Emit(name string, args ...any) {
	ev := event{Name: name, Args: args}
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	for _, ch := range s.subs {
		// Best-effort delivery: a subscriber that can't keep up drops events
		// rather than blocking the emitter.
		select {
		case ch <- ev:
		default:
		}
	}
}

// Close drains every SSE subscriber so net/http can shut the listener down
// without leaking goroutines. Safe to call multiple times.
func (s *Server) Close() {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	for id, ch := range s.subs {
		close(ch)
		delete(s.subs, id)
	}
}

// Handler returns the mux wiring every HTTP route this transport owns.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/__erun_invoke", s.handleInvoke)
	mux.HandleFunc("/__erun_events", s.handleEvents)
	mux.HandleFunc("/__erun_emit", s.handleEmit)
	mux.HandleFunc("/__erun_clipboard", s.handleClipboard)
	mux.HandleFunc("/", s.handleStatic)
	return mux
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" || path == "index.html" {
		s.serveIndex(w, r)
		return
	}
	// Missing files fall back to index.html so SPA deep links still load and the
	// React router can resolve them.
	if data, err := fs.ReadFile(s.bundle, path); err == nil {
		writeStatic(w, path, data)
		return
	}
	s.serveIndex(w, r)
}

func (s *Server) serveIndex(w http.ResponseWriter, _ *http.Request) {
	raw, err := fs.ReadFile(s.bundle, "index.html")
	if err != nil {
		http.Error(w, "index.html missing from bundle", http.StatusInternalServerError)
		return
	}
	html := injectShim(string(raw), s.shimJS)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, html)
}

func writeStatic(w http.ResponseWriter, path string, data []byte) {
	if ct := contentTypeForPath(path); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// Order matters: ".woff2" must precede ".woff" so the longer suffix wins.
var contentTypeBySuffix = []struct {
	suffix      string
	contentType string
}{
	{".js", "application/javascript; charset=utf-8"},
	{".css", "text/css; charset=utf-8"},
	{".html", "text/html; charset=utf-8"},
	{".json", "application/json; charset=utf-8"},
	{".svg", "image/svg+xml"},
	{".png", "image/png"},
	{".woff2", "font/woff2"},
	{".woff", "font/woff"},
	{".map", "application/json; charset=utf-8"},
}

func contentTypeForPath(path string) string {
	for _, entry := range contentTypeBySuffix {
		if strings.HasSuffix(path, entry.suffix) {
			return entry.contentType
		}
	}
	return ""
}

// invokeRequest keeps args as raw JSON so each is unmarshalled into the exact
// Go type its target method parameter expects.
type invokeRequest struct {
	Method string            `json:"method"`
	Args   []json.RawMessage `json:"args"`
}

type invokeEnvelope struct {
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

func (s *Server) handleInvoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req invokeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, invokeEnvelope{Error: fmt.Sprintf("invalid request: %v", err)})
		return
	}
	method, ok := s.methods[req.Method]
	if !ok {
		writeJSON(w, http.StatusNotFound, invokeEnvelope{Error: fmt.Sprintf("unknown method %q", req.Method)})
		return
	}
	out, err := callMethod(method, req.Args)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, invokeEnvelope{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, invokeEnvelope{Data: out})
}

func callMethod(method reflect.Value, rawArgs []json.RawMessage) (any, error) {
	mt := method.Type()
	wantArgs := mt.NumIn()
	if len(rawArgs) != wantArgs {
		return nil, fmt.Errorf("expected %d args, got %d", wantArgs, len(rawArgs))
	}
	in, err := decodeMethodArgs(mt, rawArgs)
	if err != nil {
		return nil, err
	}
	return mapMethodResults(method.Call(in))
}

// A missing or null arg leaves the parameter at its zero value, matching Wails'
// lenient call convention.
func decodeMethodArgs(mt reflect.Type, rawArgs []json.RawMessage) ([]reflect.Value, error) {
	in := make([]reflect.Value, mt.NumIn())
	for i := 0; i < mt.NumIn(); i++ {
		slot := reflect.New(mt.In(i))
		if len(rawArgs[i]) > 0 && string(rawArgs[i]) != "null" {
			if err := json.Unmarshal(rawArgs[i], slot.Interface()); err != nil {
				return nil, fmt.Errorf("arg %d: %v", i, err)
			}
		}
		in[i] = slot.Elem()
	}
	return in, nil
}

// mapMethodResults maps a Wails method's return values — one of (), (T),
// (error), or (T, error) — back to a {data, error?} envelope.
func mapMethodResults(results []reflect.Value) (any, error) {
	var (
		data any
		err  error
	)
	for _, r := range results {
		if r.Type().Implements(errorType) {
			if !r.IsNil() {
				err = r.Interface().(error)
			}
			continue
		}
		if data == nil {
			data = r.Interface()
		}
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

var errorType = reflect.TypeOf((*error)(nil)).Elem()

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")

	ch := make(chan event, 64)
	id := s.nextID.Add(1)
	s.subsMu.Lock()
	s.subs[id] = ch
	s.subsMu.Unlock()

	defer func() {
		s.subsMu.Lock()
		delete(s.subs, id)
		s.subsMu.Unlock()
	}()

	flusher.Flush()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

type emitRequest struct {
	Name string `json:"name"`
	Args []any  `json:"args"`
}

func (s *Server) handleEmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req emitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.Emit(req.Name, req.Args...)
	w.WriteHeader(http.StatusNoContent)
}

type clipboardRequest struct {
	Action string `json:"action"`
	Text   string `json:"text,omitempty"`
}

type clipboardResponse struct {
	Text string `json:"text"`
}

func (s *Server) handleClipboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req clipboardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.clipMu.Lock()
	defer s.clipMu.Unlock()
	switch req.Action {
	case "set":
		s.clip = req.Text
		w.WriteHeader(http.StatusNoContent)
	case "get":
		writeJSON(w, http.StatusOK, clipboardResponse{Text: s.clip})
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("headlessserver: write json: %v", err)
	}
}

// Listen serves the handler on addr and blocks until ctx is cancelled,
// returning any non-graceful shutdown error.
func (s *Server) Listen(ctx context.Context, addr string) error {
	server := &http.Server{Addr: addr, Handler: s.Handler()}
	errCh := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		s.Close()
		return nil
	case err := <-errCh:
		s.Close()
		return err
	}
}

func collectExportedMethods(target reflect.Value) (map[string]reflect.Value, []string) {
	methods := make(map[string]reflect.Value)
	t := target.Type()
	names := make([]string, 0, t.NumMethod())
	for i := 0; i < t.NumMethod(); i++ {
		m := t.Method(i)
		if !m.IsExported() {
			continue
		}
		methods[m.Name] = target.Method(i)
		names = append(names, m.Name)
	}
	return methods, names
}
