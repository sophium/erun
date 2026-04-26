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
	SaveERunConfig(eruncommon.ERunConfig) error
	SaveTenantConfig(eruncommon.TenantConfig) error
	SaveEnvConfig(string, eruncommon.EnvConfig) error
}

type erunUIDeps struct {
	store                erunUIStore
	findProjectRoot      eruncommon.ProjectFinderFunc
	resolveCLIPath       func() string
	resolveBuildInfo     func() eruncommon.BuildInfo
	resolveImageRegistry func(context.Context, string, string) (eruncommon.RuntimeRegistryVersions, error)
	deleteNamespace      eruncommon.NamespaceDeleterFunc
	startTerminal        func(startTerminalSessionParams) (terminalSession, error)
	savePastedImage      func(pastedImageSaveParams) (string, error)
	loadDiff             func(context.Context, string) (eruncommon.DiffResult, error)
	windowStatePath      string
	windowMaximised      func(context.Context) bool
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
	Tenants            []uiTenant     `json:"tenants"`
	Selected           *uiSelection   `json:"selected,omitempty"`
	Message            string         `json:"message,omitempty"`
	Build              uiBuildDetails `json:"build"`
	VersionSuggestions []uiVersion    `json:"versionSuggestions,omitempty"`
}

type uiTenant struct {
	Name         string          `json:"name"`
	Environments []uiEnvironment `json:"environments"`
}

type uiEnvironment struct {
	Name           string `json:"name"`
	MCPURL         string `json:"mcpUrl,omitempty"`
	RuntimeVersion string `json:"runtimeVersion,omitempty"`
}

type uiSelection struct {
	Tenant       string `json:"tenant"`
	Environment  string `json:"environment"`
	Version      string `json:"version,omitempty"`
	RuntimeImage string `json:"runtimeImage,omitempty"`
	NoGit        bool   `json:"noGit,omitempty"`
	Action       string `json:"action,omitempty"`
}

type uiBuildDetails struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
}

type uiVersion = eruncommon.RuntimeVersionSuggestion

type uiERunConfig struct {
	DefaultTenant string `json:"defaultTenant"`
}

type uiTenantConfig struct {
	Name               string `json:"name"`
	DefaultEnvironment string `json:"defaultEnvironment"`
}

type uiSSHDConfig struct {
	Enabled       bool   `json:"enabled"`
	LocalPort     int    `json:"localPort"`
	PublicKeyPath string `json:"publicKeyPath"`
}

type uiEnvironmentConfig struct {
	Name              string       `json:"name"`
	RepoPath          string       `json:"repoPath"`
	KubernetesContext string       `json:"kubernetesContext"`
	ContainerRegistry string       `json:"containerRegistry"`
	RuntimeVersion    string       `json:"runtimeVersion"`
	SSHD              uiSSHDConfig `json:"sshd"`
	Remote            bool         `json:"remote"`
	Snapshot          bool         `json:"snapshot"`
}

type startSessionResult struct {
	SessionID int         `json:"sessionId"`
	Selection uiSelection `json:"selection"`
}

type deleteEnvironmentResult struct {
	Tenant               string `json:"tenant"`
	Environment          string `json:"environment"`
	Namespace            string `json:"namespace,omitempty"`
	KubernetesContext    string `json:"kubernetesContext,omitempty"`
	NamespaceDeleteError string `json:"namespaceDeleteError,omitempty"`
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
	if deps.resolveBuildInfo == nil {
		deps.resolveBuildInfo = func() eruncommon.BuildInfo {
			return resolveCurrentBuildInfo(deps.resolveCLIPath)
		}
	}
	if deps.resolveImageRegistry == nil {
		deps.resolveImageRegistry = eruncommon.ResolveRuntimeImageRegistryVersions
	}
	if deps.deleteNamespace == nil {
		deps.deleteNamespace = eruncommon.DeleteKubernetesNamespace
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
			info := a.deps.resolveBuildInfo()
			return uiState{
				Message:            "ERun is not initialized yet. Run `erun init` first.",
				Build:              buildDetailsFrom(info),
				VersionSuggestions: a.runtimeVersionSuggestions(info, ""),
			}, nil
		}
		return uiState{}, err
	}
	info := a.deps.resolveBuildInfo()
	state := stateFromListResult(result, info)
	suggestionTenant := ""
	if state.Selected != nil {
		suggestionTenant = state.Selected.Tenant
	} else if len(state.Tenants) > 0 {
		suggestionTenant = state.Tenants[0].Name
	}
	state.VersionSuggestions = a.runtimeVersionSuggestions(info, suggestionTenant)
	return state, nil
}

func (a *App) resolveRuntimeRegistryVersionsForTenant(tenant string) eruncommon.RuntimeRegistryVersions {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	repository := eruncommon.DefaultRuntimeImageName
	if tenant = strings.TrimSpace(tenant); tenant != "" {
		repository = eruncommon.RuntimeReleaseName(tenant)
	}
	versions, err := a.deps.resolveImageRegistry(ctx, eruncommon.DefaultContainerRegistry, repository)
	if err != nil {
		return eruncommon.RuntimeRegistryVersions{}
	}
	return versions
}

func (a *App) runtimeVersionSuggestions(info eruncommon.BuildInfo, tenant string) []uiVersion {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return labelRuntimeVersionSuggestions("ERun", eruncommon.DefaultRuntimeImageName, eruncommon.RuntimeDeployVersionSuggestions(info, a.resolveRuntimeRegistryVersionsForTenant("")))
	}

	suggestions := make([]uiVersion, 0, 8)
	tenantImage := eruncommon.RuntimeReleaseName(tenant)
	suggestions = append(suggestions, labelRuntimeVersionSuggestions(tenant, tenantImage, eruncommon.RuntimeDeployVersionSuggestions(info, a.resolveRuntimeRegistryVersionsForTenant(tenant)))...)
	if tenantImage == eruncommon.DefaultRuntimeImageName {
		return suggestions
	}
	suggestions = append(suggestions, labelRuntimeVersionSuggestions("ERun", eruncommon.DefaultRuntimeImageName, eruncommon.RuntimeDeployVersionSuggestions(info, a.resolveRuntimeRegistryVersionsForTenant("")))...)
	return suggestions
}

func (a *App) LoadVersionSuggestions(selection uiSelection) ([]uiVersion, error) {
	selection = normalizeSelection(selection)
	if selection.Action == "init" {
		return a.runtimeVersionSuggestions(a.deps.resolveBuildInfo(), selection.Tenant), nil
	}
	return a.runtimeVersionSuggestions(a.deps.resolveBuildInfo(), selection.Tenant), nil
}

func (a *App) LoadERunConfig() (uiERunConfig, error) {
	config, _, err := a.deps.store.LoadERunConfig()
	if err != nil {
		return uiERunConfig{}, err
	}
	return erunConfigToUI(config), nil
}

func (a *App) SaveERunConfig(config uiERunConfig) (uiERunConfig, error) {
	updated := eruncommon.ERunConfig{
		DefaultTenant: strings.TrimSpace(config.DefaultTenant),
	}
	if err := a.deps.store.SaveERunConfig(updated); err != nil {
		return uiERunConfig{}, err
	}
	return erunConfigToUI(updated), nil
}

func (a *App) LoadTenantConfig(tenant string) (uiTenantConfig, error) {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return uiTenantConfig{}, fmt.Errorf("tenant is required")
	}

	config, _, err := a.deps.store.LoadTenantConfig(tenant)
	if err != nil {
		return uiTenantConfig{}, err
	}
	return tenantConfigToUI(config, tenant), nil
}

func (a *App) SaveTenantConfig(config uiTenantConfig) (uiTenantConfig, error) {
	tenant := strings.TrimSpace(config.Name)
	if tenant == "" {
		return uiTenantConfig{}, fmt.Errorf("tenant is required")
	}

	existing, _, err := a.deps.store.LoadTenantConfig(tenant)
	if err != nil {
		return uiTenantConfig{}, err
	}
	updated := tenantConfigFromUI(config, existing)
	if err := a.deps.store.SaveTenantConfig(updated); err != nil {
		return uiTenantConfig{}, err
	}
	return tenantConfigToUI(updated, tenant), nil
}

func (a *App) LoadEnvironmentConfig(selection uiSelection) (uiEnvironmentConfig, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return uiEnvironmentConfig{}, fmt.Errorf("tenant and environment are required")
	}

	config, _, err := a.deps.store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return uiEnvironmentConfig{}, err
	}
	return environmentConfigToUI(config, selection.Environment), nil
}

func (a *App) SaveEnvironmentConfig(selection uiSelection, config uiEnvironmentConfig) (uiEnvironmentConfig, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return uiEnvironmentConfig{}, fmt.Errorf("tenant and environment are required")
	}

	existing, _, err := a.deps.store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return uiEnvironmentConfig{}, err
	}
	updated := environmentConfigFromUI(config, existing)
	if err := a.deps.store.SaveEnvConfig(selection.Tenant, updated); err != nil {
		return uiEnvironmentConfig{}, err
	}
	return environmentConfigToUI(updated, selection.Environment), nil
}

func labelRuntimeVersionSuggestions(source, image string, suggestions []uiVersion) []uiVersion {
	source = strings.TrimSpace(source)
	image = strings.TrimSpace(image)
	labeled := make([]uiVersion, 0, len(suggestions))
	for _, suggestion := range suggestions {
		label := strings.TrimSpace(suggestion.Label)
		if source != "" && label != "" {
			label = source + " " + strings.ToLower(label[:1]) + label[1:]
		}
		suggestion.Label = label
		suggestion.Source = source
		suggestion.Image = image
		labeled = append(labeled, suggestion)
	}
	return labeled
}

func erunConfigToUI(config eruncommon.ERunConfig) uiERunConfig {
	return uiERunConfig{
		DefaultTenant: strings.TrimSpace(config.DefaultTenant),
	}
}

func tenantConfigToUI(config eruncommon.TenantConfig, fallbackName string) uiTenantConfig {
	name := strings.TrimSpace(config.Name)
	if name == "" {
		name = strings.TrimSpace(fallbackName)
	}
	result := uiTenantConfig{
		Name:               name,
		DefaultEnvironment: strings.TrimSpace(config.DefaultEnvironment),
	}
	return result
}

func tenantConfigFromUI(config uiTenantConfig, existing eruncommon.TenantConfig) eruncommon.TenantConfig {
	existing.Name = strings.TrimSpace(config.Name)
	existing.DefaultEnvironment = strings.TrimSpace(config.DefaultEnvironment)
	return existing
}

func environmentConfigToUI(config eruncommon.EnvConfig, fallbackName string) uiEnvironmentConfig {
	name := strings.TrimSpace(config.Name)
	if name == "" {
		name = strings.TrimSpace(fallbackName)
	}
	result := uiEnvironmentConfig{
		Name:              name,
		RepoPath:          strings.TrimSpace(config.RepoPath),
		KubernetesContext: strings.TrimSpace(config.KubernetesContext),
		ContainerRegistry: strings.TrimSpace(config.ContainerRegistry),
		RuntimeVersion:    strings.TrimSpace(config.RuntimeVersion),
		SSHD: uiSSHDConfig{
			Enabled:       config.SSHD.Enabled,
			LocalPort:     config.SSHD.LocalPort,
			PublicKeyPath: strings.TrimSpace(config.SSHD.PublicKeyPath),
		},
		Remote:   config.Remote,
		Snapshot: config.SnapshotEnabled(),
	}
	return result
}

func environmentConfigFromUI(config uiEnvironmentConfig, existing eruncommon.EnvConfig) eruncommon.EnvConfig {
	existing.Name = strings.TrimSpace(config.Name)
	existing.SetSnapshot(config.Snapshot)
	return existing
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

func (a *App) StartInitSession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	return a.startCommandSession(selection, cols, rows, initSelectionKey(selection), buildInitArgs(selection.Tenant, selection.Environment, selection.Version, selection.RuntimeImage, selection.NoGit), resolveInitStartDir(a.deps.findProjectRoot), []string{appSessionEnvVar + "=1"})
}

func (a *App) StartDeploySession(selection uiSelection, cols, rows int) (startSessionResult, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return startSessionResult{}, fmt.Errorf("tenant and environment are required")
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return startSessionResult{}, err
	}
	return a.startCommandSession(selection, cols, rows, deploySelectionKey(selection), buildDeployArgs(selection.Tenant, selection.Environment, selection.Version, selection.RuntimeImage), resolveDeployStartDir(a.deps.findProjectRoot, result), []string{appSessionEnvVar + "=1"})
}

func (a *App) DeleteEnvironment(selection uiSelection, confirmation string) (deleteEnvironmentResult, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return deleteEnvironmentResult{}, fmt.Errorf("tenant and environment are required")
	}
	expected := eruncommon.DeleteEnvironmentConfirmation(selection.Tenant, selection.Environment)
	if strings.TrimSpace(confirmation) != expected {
		return deleteEnvironmentResult{}, fmt.Errorf("delete confirmation did not match %q", expected)
	}

	store, ok := a.deps.store.(eruncommon.DeleteStore)
	if !ok {
		return deleteEnvironmentResult{}, fmt.Errorf("environment deletion is not supported by the configured store")
	}

	result, err := eruncommon.RunDeleteEnvironment(eruncommon.Context{}, eruncommon.DeleteEnvironmentParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	}, store, a.deps.deleteNamespace)
	if err != nil {
		return deleteEnvironmentResult{}, err
	}
	a.closeSessionsForSelection(selection)
	return deleteEnvironmentResult{
		Tenant:               result.Tenant,
		Environment:          result.Environment,
		Namespace:            result.Namespace,
		KubernetesContext:    result.KubernetesContext,
		NamespaceDeleteError: result.NamespaceDeleteError,
	}, nil
}

func (a *App) startCommandSession(selection uiSelection, cols, rows int, key string, args []string, dir string, env []string) (startSessionResult, error) {
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

	session, err := a.deps.startTerminal(startTerminalSessionParams{
		Dir:        dir,
		Executable: a.deps.resolveCLIPath(),
		Args:       args,
		Env:        env,
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
			reason := terminalSessionExitReason(managed.session, err)
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

func terminalSessionExitReason(session terminalSession, readErr error) string {
	if session != nil {
		if waitErr := session.Wait(); waitErr != nil {
			return waitErr.Error()
		}
		return ""
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return readErr.Error()
	}
	return ""
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

func (a *App) closeSessionsForSelection(selection uiSelection) {
	selection = normalizeSelection(selection)
	prefixes := []string{
		selectionKey(selection),
		"init\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00",
		"deploy\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00",
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for key, session := range a.sessions {
		if session == nil {
			continue
		}
		matches := false
		for _, prefix := range prefixes {
			if key == prefix || strings.HasPrefix(key, prefix) {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		_ = session.Close()
		delete(a.sessions, key)
		if a.current == session {
			a.current = nil
		}
	}
}

func stateFromListResult(result eruncommon.ListResult, info eruncommon.BuildInfo) uiState {
	state := uiState{
		Tenants: make([]uiTenant, 0, len(result.Tenants)),
		Build:   buildDetailsFrom(info),
	}
	for _, tenant := range result.Tenants {
		if len(tenant.Environments) == 0 {
			continue
		}
		item := uiTenant{
			Name:         strings.TrimSpace(tenant.Name),
			Environments: make([]uiEnvironment, 0, len(tenant.Environments)),
		}
		for _, environment := range tenant.Environments {
			item.Environments = append(item.Environments, uiEnvironment{
				Name:           strings.TrimSpace(environment.Name),
				MCPURL:         mcpEndpointForListEnvironment(environment),
				RuntimeVersion: strings.TrimSpace(environment.RuntimeVersion),
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
		Tenant:       strings.TrimSpace(selection.Tenant),
		Environment:  strings.TrimSpace(selection.Environment),
		Version:      strings.TrimSpace(selection.Version),
		RuntimeImage: strings.TrimSpace(selection.RuntimeImage),
		NoGit:        selection.NoGit,
		Action:       strings.TrimSpace(selection.Action),
	}
}

func resolveInitStartDir(findProjectRoot eruncommon.ProjectFinderFunc) string {
	if findProjectRoot != nil {
		if _, projectRoot, err := findProjectRoot(); err == nil && strings.TrimSpace(projectRoot) != "" {
			return resolveTerminalStartDir(projectRoot)
		}
	}
	return resolveTerminalStartDir("")
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

func initSelectionKey(selection uiSelection) string {
	selection = normalizeSelection(selection)
	return "init\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00" + selection.Version + "\x00" + selection.RuntimeImage + "\x00" + fmt.Sprintf("%t", selection.NoGit)
}

func deploySelectionKey(selection uiSelection) string {
	selection = normalizeSelection(selection)
	return "deploy\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00" + selection.Version + "\x00" + selection.RuntimeImage
}
