package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	terminalOutputEvent = "terminal-output"
	terminalExitEvent   = "terminal-exit"
	appSessionEnvVar    = "ERUN_UI_SESSION"
)

type erunUIStore interface {
	eruncommon.ListStore
}

type erunUIDeps struct {
	store           erunUIStore
	findProjectRoot eruncommon.ProjectFinderFunc
	resolveCLIPath  func() string
	startTerminal   func(startTerminalSessionParams) (terminalSession, error)
	savePastedImage func(pastedImageSaveParams) (string, error)
	loadDiff        func(context.Context, string) (eruncommon.DiffResult, error)
	windowStatePath string
	windowMaximised func(context.Context) bool
}

type App struct {
	ctx  context.Context
	deps erunUIDeps

	mu         sync.Mutex
	current    *managedTerminal
	nextSerial int
	sessions   map[string]*managedTerminal
}

type uiState struct {
	Tenants  []uiTenant     `json:"tenants"`
	Selected *uiSelection   `json:"selected,omitempty"`
	Message  string         `json:"message,omitempty"`
	Build    uiBuildDetails `json:"build"`
}

type uiTenant struct {
	Name         string          `json:"name"`
	Environments []uiEnvironment `json:"environments"`
}

type uiEnvironment struct {
	Name   string `json:"name"`
	MCPURL string `json:"mcpUrl,omitempty"`
}

type uiSelection struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
}

type uiBuildDetails struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
}

type startSessionResult struct {
	SessionID int         `json:"sessionId"`
	Selection uiSelection `json:"selection"`
}

type terminalOutputPayload struct {
	SessionID int    `json:"sessionId"`
	Data      string `json:"data"`
}

type terminalExitPayload struct {
	SessionID int    `json:"sessionId"`
	Reason    string `json:"reason,omitempty"`
}

type pastedImagePayload struct {
	Data     string `json:"data"`
	MIMEType string `json:"mimeType,omitempty"`
	Name     string `json:"name,omitempty"`
}

type pastedImageResult struct {
	Path string `json:"path"`
}

func NewApp(deps erunUIDeps) *App {
	if deps.store == nil {
		deps.store = eruncommon.ConfigStore{}
	}
	if deps.findProjectRoot == nil {
		deps.findProjectRoot = eruncommon.FindProjectRoot
	}
	if deps.resolveCLIPath == nil {
		deps.resolveCLIPath = resolveCLIExecutable
	}
	if deps.startTerminal == nil {
		deps.startTerminal = startTerminalSession
	}
	if deps.savePastedImage == nil {
		deps.savePastedImage = savePastedImageToRuntime
	}
	if deps.loadDiff == nil {
		deps.loadDiff = loadDiffFromMCP
	}
	if deps.windowStatePath == "" {
		deps.windowStatePath = defaultAppWindowStatePath()
	}
	if deps.windowMaximised == nil {
		deps.windowMaximised = runtime.WindowIsMaximised
	}
	return &App{
		deps:     deps,
		sessions: make(map[string]*managedTerminal),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	configureAppIdentity("ERun")
}

func (a *App) shutdown(context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closeAllSessionsLocked()
}

func (a *App) beforeClose(ctx context.Context) bool {
	_ = saveAppWindowState(a.deps.windowStatePath, appWindowState{
		Maximised: a.deps.windowMaximised(ctx),
	})
	return false
}

func (a *App) LoadState() (uiState, error) {
	result, err := eruncommon.ResolveListResult(a.deps.store, a.deps.findProjectRoot, eruncommon.OpenParams{
		UseDefaultTenant:      true,
		UseDefaultEnvironment: true,
	})
	if err != nil {
		if errors.Is(err, eruncommon.ErrNotInitialized) {
			return uiState{
				Message: "ERun is not initialized yet. Run `erun init` first.",
				Build:   buildDetailsFrom(currentBuildInfo()),
			}, nil
		}
		return uiState{}, err
	}
	return stateFromListResult(result), nil
}

func (a *App) StartSession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return startSessionResult{}, fmt.Errorf("tenant and environment are required")
	}

	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 34
	}

	key := selectionKey(selection)

	a.mu.Lock()
	if existing := a.sessions[key]; existing != nil && !existing.closed && existing.session != nil {
		a.current = existing
		a.mu.Unlock()
		return startSessionResult{
			SessionID: existing.serial,
			Selection: existing.selection,
		}, nil
	}
	a.mu.Unlock()

	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return startSessionResult{}, err
	}

	session, err := a.deps.startTerminal(startTerminalSessionParams{
		Dir:        resolveTerminalStartDir(result.RepoPath),
		Executable: a.deps.resolveCLIPath(),
		Args:       buildOpenArgs(result.Tenant, result.Environment),
		Env:        []string{appSessionEnvVar + "=1"},
		Cols:       cols,
		Rows:       rows,
	})
	if err != nil {
		return startSessionResult{}, err
	}

	a.mu.Lock()
	a.nextSerial++
	serial := a.nextSerial
	managed := &managedTerminal{
		session:   session,
		selection: selection,
		key:       key,
		serial:    serial,
	}
	a.sessions[key] = managed
	a.current = managed
	a.mu.Unlock()

	go a.streamSession(managed)

	return startSessionResult{
		SessionID: serial,
		Selection: selection,
	}, nil
}

func (a *App) SendSessionInput(data string) error {
	if data == "" {
		return nil
	}

	a.mu.Lock()
	current := a.current
	a.mu.Unlock()
	if current == nil || current.session == nil {
		return nil
	}

	_, err := io.WriteString(current.session, data)
	return err
}

func (a *App) SavePastedImage(payload pastedImagePayload) (pastedImageResult, error) {
	data, mimeType, err := decodePastedImagePayload(payload)
	if err != nil {
		return pastedImageResult{}, err
	}

	a.mu.Lock()
	current := a.current
	a.mu.Unlock()
	if current == nil || current.session == nil {
		return pastedImageResult{}, fmt.Errorf("no active terminal session")
	}

	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      current.selection.Tenant,
		Environment: current.selection.Environment,
	})
	if err != nil {
		return pastedImageResult{}, err
	}

	path, err := a.deps.savePastedImage(pastedImageSaveParams{
		Result:   result,
		Data:     data,
		MIMEType: mimeType,
		Name:     payload.Name,
	})
	if err != nil {
		return pastedImageResult{}, err
	}
	return pastedImageResult{Path: path}, nil
}

func (a *App) LoadDiff(selection uiSelection) (eruncommon.DiffResult, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return eruncommon.DiffResult{}, fmt.Errorf("tenant and environment are required")
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return eruncommon.DiffResult{}, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return a.deps.loadDiff(ctx, mcpEndpointForOpenResult(result))
}

func (a *App) ResizeSession(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}

	a.mu.Lock()
	current := a.current
	a.mu.Unlock()
	if current == nil || current.session == nil {
		return nil
	}

	return current.session.Resize(cols, rows)
}

func decodePastedImagePayload(payload pastedImagePayload) ([]byte, string, error) {
	value := strings.TrimSpace(payload.Data)
	mimeType := strings.TrimSpace(payload.MIMEType)
	if strings.HasPrefix(value, "data:") {
		header, body, ok := strings.Cut(value, ",")
		if !ok {
			return nil, "", fmt.Errorf("pasted image data URL is malformed")
		}
		value = body
		if mimeType == "" {
			mediaType := strings.TrimPrefix(header, "data:")
			mediaType, _, _ = strings.Cut(mediaType, ";")
			mimeType = strings.TrimSpace(mediaType)
		}
	}
	if value == "" {
		return nil, "", fmt.Errorf("pasted image data is empty")
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, "", fmt.Errorf("decode pasted image: %w", err)
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("pasted image data is empty")
	}
	if !strings.HasPrefix(strings.ToLower(mimeType), "image/") {
		return nil, "", fmt.Errorf("clipboard item is not an image")
	}
	return data, mimeType, nil
}

func (a *App) streamSession(managed *managedTerminal) {
	buffer := make([]byte, 8192)
	for {
		count, err := managed.session.Read(buffer)
		if count > 0 {
			payload := terminalOutputPayload{
				SessionID: managed.serial,
				Data:      base64.StdEncoding.EncodeToString(buffer[:count]),
			}
			a.emitEvent(terminalOutputEvent, payload)
		}
		if err != nil {
			reason := ""
			if !errors.Is(err, io.EOF) {
				reason = err.Error()
			}
			a.mu.Lock()
			managed.closed = true
			if existing := a.sessions[managed.key]; existing == managed {
				delete(a.sessions, managed.key)
			}
			if a.current == managed {
				a.current = nil
			}
			a.mu.Unlock()
			a.emitEvent(terminalExitEvent, terminalExitPayload{
				SessionID: managed.serial,
				Reason:    reason,
			})
			return
		}
	}
}

func (a *App) emitEvent(name string, payload any) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, name, payload)
}

func (a *App) closeAllSessionsLocked() {
	closed := make(map[*managedTerminal]struct{}, len(a.sessions))
	for _, session := range a.sessions {
		if session == nil {
			continue
		}
		if _, seen := closed[session]; seen {
			continue
		}
		closed[session] = struct{}{}
		_ = session.Close()
	}
	if a.current != nil {
		if _, seen := closed[a.current]; !seen {
			_ = a.current.Close()
		}
	}
	a.sessions = make(map[string]*managedTerminal)
	a.current = nil
}

func stateFromListResult(result eruncommon.ListResult) uiState {
	state := uiState{
		Tenants: make([]uiTenant, 0, len(result.Tenants)),
		Build:   buildDetailsFrom(currentBuildInfo()),
	}
	for _, tenant := range result.Tenants {
		item := uiTenant{
			Name:         strings.TrimSpace(tenant.Name),
			Environments: make([]uiEnvironment, 0, len(tenant.Environments)),
		}
		for _, environment := range tenant.Environments {
			item.Environments = append(item.Environments, uiEnvironment{
				Name:   strings.TrimSpace(environment.Name),
				MCPURL: mcpEndpointForListEnvironment(environment),
			})
		}
		state.Tenants = append(state.Tenants, item)
	}
	if result.CurrentDirectory.Effective != nil {
		state.Selected = &uiSelection{
			Tenant:      strings.TrimSpace(result.CurrentDirectory.Effective.Tenant),
			Environment: strings.TrimSpace(result.CurrentDirectory.Effective.Environment),
		}
	}
	return state
}

func mcpEndpointForOpenResult(result eruncommon.OpenResult) string {
	return fmt.Sprintf("http://127.0.0.1:%d/mcp", eruncommon.MCPPortForResult(result))
}

func mcpEndpointForListEnvironment(environment eruncommon.ListEnvironmentResult) string {
	port := environment.LocalPorts.MCP
	if port <= 0 {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
}

func buildDetailsFrom(info eruncommon.BuildInfo) uiBuildDetails {
	return uiBuildDetails{
		Version: info.Version,
		Commit:  info.Commit,
		Date:    info.Date,
	}
}

func normalizeSelection(selection uiSelection) uiSelection {
	return uiSelection{
		Tenant:      strings.TrimSpace(selection.Tenant),
		Environment: strings.TrimSpace(selection.Environment),
	}
}

type managedTerminal struct {
	session   terminalSession
	selection uiSelection
	key       string
	serial    int
	closed    bool
}

func (s *managedTerminal) Close() error {
	if s == nil || s.session == nil {
		return nil
	}
	s.closed = true
	return s.session.Close()
}

func selectionKey(selection uiSelection) string {
	selection = normalizeSelection(selection)
	return selection.Tenant + "\x00" + selection.Environment
}
