// Package headlessserver serves the desktop frontend over HTTP with a shim for
// the Wails runtime, so headless clients can drive the unmodified bundle.
package headlessserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
)

// Server is the HTTP transport for headless mode; construct it with New.
type Server struct {
	target reflect.Value
	bundle fs.FS

	methods    map[string]reflect.Value
	methodList []string
	shimJS     string

	subsMu sync.Mutex
	subs   map[int64]*subscriber
	nextID atomic.Int64

	clipMu sync.Mutex
	clip   string
}

type subscriber struct {
	ch     chan event
	missed atomic.Int64
}

type event struct {
	Name string `json:"name"`
	Args []any  `json:"args"`
}

// eventsDroppedName replaces an event a full subscriber channel could not hold.
const eventsDroppedName = "erun:events-dropped"

// New builds a Server binding target's methods by exact name, as Wails does.
func New(target any, bundle fs.FS) *Server {
	s := &Server{
		target: reflect.ValueOf(target),
		bundle: bundle,
		subs:   make(map[int64]*subscriber),
	}
	s.methods, s.methodList = collectExportedMethods(s.target)
	s.shimJS = buildShimJS(s.methodList)
	return s
}

// Emit fans an event out to every SSE subscriber. A full subscriber loses its
// oldest event to an eventsDroppedName marker, never silently.
func (s *Server) Emit(name string, args ...any) {
	ev := event{Name: name, Args: args}
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	for _, sub := range s.subs {
		select {
		case sub.ch <- ev:
			continue
		default:
		}
		missed := sub.missed.Add(1)
		log.Printf("headlessserver: subscriber channel full, dropped event %q (missed=%d)", name, missed)
		// Emit is the sole producer and holds subsMu, so the freed slot stays free.
		select {
		case <-sub.ch:
		default:
		}
		gap := event{Name: eventsDroppedName, Args: []any{missed}}
		select {
		case sub.ch <- gap:
		default:
			log.Printf("headlessserver: dropped gap marker for subscriber (missed=%d)", missed)
		}
	}
}

// Close drains every SSE subscriber; safe to call multiple times.
func (s *Server) Close() {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	for id, sub := range s.subs {
		close(sub.ch)
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
	// SPA fallback so deep links load.
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

// ".woff2" must precede ".woff" so the longer suffix wins.
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

// A missing or null arg stays zero, matching Wails' lenient convention.
func decodeMethodArgs(mt reflect.Type, rawArgs []json.RawMessage) ([]reflect.Value, error) {
	in := make([]reflect.Value, mt.NumIn())
	for i := 0; i < mt.NumIn(); i++ {
		slot := reflect.New(mt.In(i))
		if len(rawArgs[i]) > 0 && string(rawArgs[i]) != "null" {
			if err := json.Unmarshal(rawArgs[i], slot.Interface()); err != nil {
				return nil, fmt.Errorf("arg %d: %w", i, err)
			}
		}
		in[i] = slot.Elem()
	}
	return in, nil
}

func mapMethodResults(results []reflect.Value) (any, error) {
	var (
		data any
		err  error
	)
	for _, r := range results {
		if r.Type().Implements(errorType) {
			if callErr, ok := r.Interface().(error); ok && !r.IsNil() {
				err = callErr
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

	sub := &subscriber{ch: make(chan event, 64)}
	ch := sub.ch
	id := s.nextID.Add(1)
	s.subsMu.Lock()
	s.subs[id] = sub
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

// Serve serves on an already-bound listener until ctx is cancelled; the caller
// binds so it can announce the actual address.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	server := &http.Server{Handler: s.Handler()}
	errCh := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
