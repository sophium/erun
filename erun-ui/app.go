package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	terminalOutputEvent = "terminal-output"
	terminalExitEvent   = "terminal-exit"
	appStatusEvent      = "app-status"
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
	cloudContextDeps     eruncommon.CloudContextDependencies
	deleteNamespace      eruncommon.NamespaceDeleterFunc
	listKubeContexts     func() ([]string, error)
	ensureMCP            func(context.Context, eruncommon.OpenResult) error
	canConnectLocalPort  func(int) bool
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
	Tenant            string `json:"tenant"`
	Environment       string `json:"environment"`
	Version           string `json:"version,omitempty"`
	RuntimeImage      string `json:"runtimeImage,omitempty"`
	KubernetesContext string `json:"kubernetesContext,omitempty"`
	ContainerRegistry string `json:"containerRegistry,omitempty"`
	NoGit             bool   `json:"noGit,omitempty"`
	Bootstrap         bool   `json:"bootstrap,omitempty"`
	SetDefaultTenant  bool   `json:"setDefaultTenant,omitempty"`
	Action            string `json:"action,omitempty"`
	Debug             bool   `json:"debug,omitempty"`
}

type uiBuildDetails struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
}

type uiVersion = eruncommon.RuntimeVersionSuggestion

type uiERunConfig struct {
	DefaultTenant  string                  `json:"defaultTenant"`
	CloudProviders []uiCloudProviderStatus `json:"cloudProviders,omitempty"`
	CloudContexts  []uiCloudContextStatus  `json:"cloudContexts,omitempty"`
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

type uiEnvironmentLocalPorts struct {
	RangeStart int          `json:"rangeStart"`
	RangeEnd   int          `json:"rangeEnd"`
	MCP        int          `json:"mcp"`
	SSH        int          `json:"ssh"`
	MCPStatus  uiPortStatus `json:"mcpStatus"`
	SSHStatus  uiPortStatus `json:"sshStatus"`
}

type uiPortStatus struct {
	Available bool   `json:"available"`
	Status    string `json:"status"`
}

type uiEnvironmentConfig struct {
	Name               string                  `json:"name"`
	RepoPath           string                  `json:"repoPath"`
	KubernetesContext  string                  `json:"kubernetesContext"`
	ContainerRegistry  string                  `json:"containerRegistry"`
	CloudProviderAlias string                  `json:"cloudProviderAlias"`
	CloudContext       *uiCloudContextStatus   `json:"cloudContext,omitempty"`
	RuntimeVersion     string                  `json:"runtimeVersion"`
	SSHD               uiSSHDConfig            `json:"sshd"`
	LocalPorts         uiEnvironmentLocalPorts `json:"localPorts"`
	Remote             bool                    `json:"remote"`
	Snapshot           bool                    `json:"snapshot"`
}

type uiCloudProviderStatus struct {
	Alias     string `json:"alias"`
	Provider  string `json:"provider"`
	Username  string `json:"username,omitempty"`
	AccountID string `json:"accountId,omitempty"`
	Profile   string `json:"profile,omitempty"`
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
}

type uiCloudContextStatus struct {
	Name               string `json:"name"`
	Provider           string `json:"provider"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	Region             string `json:"region"`
	InstanceID         string `json:"instanceId,omitempty"`
	PublicIP           string `json:"publicIp,omitempty"`
	InstanceType       string `json:"instanceType"`
	DiskType           string `json:"diskType"`
	DiskSizeGB         int    `json:"diskSizeGb"`
	KubernetesContext  string `json:"kubernetesContext"`
	Status             string `json:"status"`
	Message            string `json:"message,omitempty"`
}

type uiAWSCloudAliasInput struct {
	Alias       string `json:"alias,omitempty"`
	Username    string `json:"username,omitempty"`
	AccountID   string `json:"accountId,omitempty"`
	Profile     string `json:"profile,omitempty"`
	SSORegion   string `json:"ssoRegion,omitempty"`
	SSOStartURL string `json:"ssoStartUrl,omitempty"`
}

type uiCloudContextInitInput struct {
	Name               string `json:"name,omitempty"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	Region             string `json:"region"`
	InstanceType       string `json:"instanceType"`
	DiskType           string `json:"diskType"`
	DiskSizeGB         int    `json:"diskSizeGb"`
}

type startSessionResult struct {
	SessionID int         `json:"sessionId"`
	Selection uiSelection `json:"selection"`
}

type deleteEnvironmentResult struct {
	Tenant                string `json:"tenant"`
	Environment           string `json:"environment"`
	Namespace             string `json:"namespace,omitempty"`
	KubernetesContext     string `json:"kubernetesContext,omitempty"`
	NamespaceDeleteError  string `json:"namespaceDeleteError,omitempty"`
	CloudContextStopError string `json:"cloudContextStopError,omitempty"`
}

type terminalOutputPayload struct {
	SessionID int    `json:"sessionId"`
	Data      string `json:"data"`
}

type terminalExitPayload struct {
	SessionID int    `json:"sessionId"`
	Reason    string `json:"reason,omitempty"`
}

type appStatusPayload struct {
	Message string `json:"message"`
	Busy    bool   `json:"busy"`
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
	if deps.listKubeContexts == nil {
		deps.listKubeContexts = listKubernetesContexts
	}
	if deps.ensureMCP == nil {
		deps.ensureMCP = func(ctx context.Context, result eruncommon.OpenResult) error {
			return ensureMCPViaOpenCommand(ctx, deps.resolveCLIPath(), result)
		}
	}
	if deps.canConnectLocalPort == nil {
		deps.canConnectLocalPort = canConnectLocalTCP
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

func (a *App) LoadKubernetesContexts() ([]string, error) {
	contexts, err := a.deps.listKubeContexts()
	if err != nil {
		return nil, err
	}
	return normalizeKubernetesContexts(contexts), nil
}

func (a *App) LoadERunConfig() (uiERunConfig, error) {
	config, _, err := a.deps.store.LoadERunConfig()
	if err != nil {
		return uiERunConfig{}, err
	}
	return erunConfigToUI(config), nil
}

func (a *App) SaveERunConfig(config uiERunConfig) (uiERunConfig, error) {
	existing, _, err := a.deps.store.LoadERunConfig()
	if errors.Is(err, eruncommon.ErrNotInitialized) {
		existing = eruncommon.ERunConfig{}
	} else if err != nil {
		return uiERunConfig{}, err
	}
	updated := eruncommon.ERunConfig{
		DefaultTenant:  strings.TrimSpace(config.DefaultTenant),
		CloudProviders: existing.CloudProviders,
		CloudContexts:  existing.CloudContexts,
	}
	if err := a.deps.store.SaveERunConfig(updated); err != nil {
		return uiERunConfig{}, err
	}
	return erunConfigToUI(updated), nil
}

func (a *App) LoadCloudProviderStatuses() ([]uiCloudProviderStatus, error) {
	statuses, err := eruncommon.ListCloudProviderStatuses(a.deps.store, eruncommon.CloudDependencies{})
	if err != nil {
		return nil, err
	}
	return cloudProviderStatusesToUI(statuses), nil
}

func (a *App) LoadCloudContextStatuses() ([]uiCloudContextStatus, error) {
	statuses, err := eruncommon.ListCloudContextStatuses(a.deps.store)
	if err != nil {
		return nil, err
	}
	return cloudContextStatusesToUI(statuses), nil
}

func (a *App) InitCloudContext(input uiCloudContextInitInput) (uiCloudContextStatus, error) {
	status, err := eruncommon.InitCloudContext(eruncommon.Context{}, a.deps.store, eruncommon.InitCloudContextParams{
		Name:               strings.TrimSpace(input.Name),
		CloudProviderAlias: strings.TrimSpace(input.CloudProviderAlias),
		Region:             strings.TrimSpace(input.Region),
		InstanceType:       strings.TrimSpace(input.InstanceType),
		DiskType:           strings.TrimSpace(input.DiskType),
		DiskSizeGB:         input.DiskSizeGB,
	}, eruncommon.CloudContextDependencies{})
	if err != nil {
		return uiCloudContextStatus{}, err
	}
	return cloudContextStatusToUI(status), nil
}

func (a *App) StopCloudContext(name string) (uiCloudContextStatus, error) {
	status, err := eruncommon.StopCloudContext(eruncommon.Context{}, a.deps.store, eruncommon.CloudContextParams{Name: name}, a.deps.cloudContextDeps)
	if err != nil {
		return uiCloudContextStatus{}, err
	}
	return cloudContextStatusToUI(status), nil
}

func (a *App) StartCloudContext(name string) (uiCloudContextStatus, error) {
	status, err := eruncommon.StartCloudContext(eruncommon.Context{}, a.deps.store, eruncommon.CloudContextParams{Name: name}, a.deps.cloudContextDeps)
	if err != nil {
		return uiCloudContextStatus{}, err
	}
	return cloudContextStatusToUI(status), nil
}

func (a *App) SaveAWSCloudProviderAlias(input uiAWSCloudAliasInput) (uiCloudProviderStatus, error) {
	provider, err := eruncommon.SaveCloudProviderConfig(a.deps.store, eruncommon.CloudProviderConfig{
		Alias:       strings.TrimSpace(input.Alias),
		Provider:    eruncommon.CloudProviderAWS,
		Username:    strings.TrimSpace(input.Username),
		AccountID:   strings.TrimSpace(input.AccountID),
		Profile:     strings.TrimSpace(input.Profile),
		SSORegion:   strings.TrimSpace(input.SSORegion),
		SSOStartURL: strings.TrimSpace(input.SSOStartURL),
	})
	if err != nil {
		return uiCloudProviderStatus{}, err
	}
	return cloudProviderStatusToUI(eruncommon.CloudProviderTokenStatus(provider, eruncommon.CloudDependencies{})), nil
}

func (a *App) InitAWSCloudProvider(input uiAWSCloudAliasInput) (uiCloudProviderStatus, error) {
	if strings.TrimSpace(input.Username) != "" || strings.TrimSpace(input.AccountID) != "" {
		return a.SaveAWSCloudProviderAlias(input)
	}
	provider, err := eruncommon.InitAWSCloudProvider(eruncommon.Context{}, a.deps.store, eruncommon.InitAWSCloudProviderParams{
		Profile: strings.TrimSpace(input.Profile),
	}, eruncommon.CloudDependencies{})
	if err != nil {
		return uiCloudProviderStatus{}, err
	}
	return cloudProviderStatusToUI(eruncommon.CloudProviderTokenStatus(provider, eruncommon.CloudDependencies{})), nil
}

func (a *App) LoginCloudProvider(alias string) (uiCloudProviderStatus, error) {
	status, err := eruncommon.LoginCloudProviderAlias(eruncommon.Context{}, a.deps.store, eruncommon.CloudLoginParams{Alias: alias}, eruncommon.CloudDependencies{})
	if err != nil {
		return uiCloudProviderStatus{}, err
	}
	return cloudProviderStatusToUI(status), nil
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
	ports, err := eruncommon.ResolveEnvironmentLocalPorts(a.deps.store, selection.Tenant, selection.Environment)
	if err != nil {
		return uiEnvironmentConfig{}, err
	}
	return a.environmentConfigToUI(config, selection.Environment, ports)
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
	ports, err := eruncommon.ResolveEnvironmentLocalPorts(a.deps.store, selection.Tenant, selection.Environment)
	if err != nil {
		return uiEnvironmentConfig{}, err
	}
	return a.environmentConfigToUI(updated, selection.Environment, ports)
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
		DefaultTenant:  strings.TrimSpace(config.DefaultTenant),
		CloudProviders: cloudProviderStatusesToUI(statusesForCloudProviders(config.CloudProviders)),
		CloudContexts:  cloudContextStatusesToUI(statusesForCloudContexts(config.CloudContexts)),
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

func (a *App) environmentConfigToUI(config eruncommon.EnvConfig, fallbackName string, ports eruncommon.EnvironmentLocalPorts) (uiEnvironmentConfig, error) {
	name := strings.TrimSpace(config.Name)
	if name == "" {
		name = strings.TrimSpace(fallbackName)
	}
	ports = eruncommon.LocalPortsForResult(eruncommon.OpenResult{
		EnvConfig:  config,
		LocalPorts: ports,
	})
	result := uiEnvironmentConfig{
		Name:               name,
		RepoPath:           strings.TrimSpace(config.RepoPath),
		KubernetesContext:  strings.TrimSpace(config.KubernetesContext),
		ContainerRegistry:  strings.TrimSpace(config.ContainerRegistry),
		CloudProviderAlias: strings.TrimSpace(config.CloudProviderAlias),
		RuntimeVersion:     strings.TrimSpace(config.RuntimeVersion),
		SSHD: uiSSHDConfig{
			Enabled:       config.SSHD.Enabled,
			LocalPort:     config.SSHD.LocalPort,
			PublicKeyPath: strings.TrimSpace(config.SSHD.PublicKeyPath),
		},
		LocalPorts: uiEnvironmentLocalPorts{
			RangeStart: ports.RangeStart,
			RangeEnd:   ports.RangeEnd,
			MCP:        ports.MCP,
			SSH:        ports.SSH,
			MCPStatus:  localPortStatus(ports.MCP),
			SSHStatus:  localPortStatus(ports.SSH),
		},
		Remote:   config.Remote,
		Snapshot: config.SnapshotEnabled(),
	}
	if cloudContext, ok, err := a.linkedCloudContext(config); err != nil {
		return uiEnvironmentConfig{}, err
	} else if ok {
		status := cloudContextStatusToUI(cloudContext)
		result.CloudContext = &status
	}
	return result, nil
}

func localPortStatus(port int) uiPortStatus {
	if port <= 0 {
		return uiPortStatus{Status: "Not assigned"}
	}
	if !canConnectLocalTCP(port) {
		return uiPortStatus{Status: "No"}
	}
	return uiPortStatus{Available: true, Status: "Yes"}
}

func canConnectLocalTCP(port int) bool {
	if port <= 0 {
		return false
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func environmentConfigFromUI(config uiEnvironmentConfig, existing eruncommon.EnvConfig) eruncommon.EnvConfig {
	existing.Name = strings.TrimSpace(config.Name)
	existing.CloudProviderAlias = strings.TrimSpace(config.CloudProviderAlias)
	existing.SetSnapshot(config.Snapshot)
	return existing
}

func statusesForCloudProviders(providers []eruncommon.CloudProviderConfig) []eruncommon.CloudProviderStatus {
	statuses := make([]eruncommon.CloudProviderStatus, 0, len(providers))
	for _, provider := range providers {
		statuses = append(statuses, eruncommon.CloudProviderTokenStatus(provider, eruncommon.CloudDependencies{}))
	}
	return statuses
}

func cloudProviderStatusesToUI(statuses []eruncommon.CloudProviderStatus) []uiCloudProviderStatus {
	result := make([]uiCloudProviderStatus, 0, len(statuses))
	for _, status := range statuses {
		result = append(result, cloudProviderStatusToUI(status))
	}
	return result
}

func cloudProviderStatusToUI(status eruncommon.CloudProviderStatus) uiCloudProviderStatus {
	return uiCloudProviderStatus{
		Alias:     strings.TrimSpace(status.Alias),
		Provider:  strings.TrimSpace(status.Provider),
		Username:  strings.TrimSpace(status.Username),
		AccountID: strings.TrimSpace(status.AccountID),
		Profile:   strings.TrimSpace(status.Profile),
		Status:    strings.TrimSpace(status.Status),
		Message:   strings.TrimSpace(status.Message),
	}
}

func statusesForCloudContexts(contexts []eruncommon.CloudContextConfig) []eruncommon.CloudContextStatus {
	statuses := make([]eruncommon.CloudContextStatus, 0, len(contexts))
	for _, context := range contexts {
		statuses = append(statuses, eruncommon.CloudContextStatus{CloudContextConfig: eruncommon.NormalizeCloudContextConfig(context)})
	}
	return statuses
}

func cloudContextStatusesToUI(statuses []eruncommon.CloudContextStatus) []uiCloudContextStatus {
	result := make([]uiCloudContextStatus, 0, len(statuses))
	for _, status := range statuses {
		result = append(result, cloudContextStatusToUI(status))
	}
	return result
}

func cloudContextStatusToUI(status eruncommon.CloudContextStatus) uiCloudContextStatus {
	context := eruncommon.NormalizeCloudContextConfig(status.CloudContextConfig)
	return uiCloudContextStatus{
		Name:               strings.TrimSpace(context.Name),
		Provider:           strings.TrimSpace(context.Provider),
		CloudProviderAlias: strings.TrimSpace(context.CloudProviderAlias),
		Region:             strings.TrimSpace(context.Region),
		InstanceID:         strings.TrimSpace(context.InstanceID),
		PublicIP:           strings.TrimSpace(context.PublicIP),
		InstanceType:       strings.TrimSpace(context.InstanceType),
		DiskType:           strings.TrimSpace(context.DiskType),
		DiskSizeGB:         context.DiskSizeGB,
		KubernetesContext:  strings.TrimSpace(context.KubernetesContext),
		Status:             strings.TrimSpace(context.Status),
		Message:            strings.TrimSpace(status.Message),
	}
}

func (a *App) linkedCloudContext(config eruncommon.EnvConfig) (eruncommon.CloudContextStatus, bool, error) {
	cloudProviderAlias := strings.TrimSpace(config.CloudProviderAlias)
	kubernetesContext := strings.TrimSpace(config.KubernetesContext)
	if kubernetesContext == "" {
		return eruncommon.CloudContextStatus{}, false, nil
	}
	statuses, err := eruncommon.ListCloudContextStatuses(a.deps.store)
	if err != nil {
		return eruncommon.CloudContextStatus{}, false, err
	}
	for _, status := range statuses {
		context := eruncommon.NormalizeCloudContextConfig(status.CloudContextConfig)
		if cloudProviderAlias != "" && strings.TrimSpace(context.CloudProviderAlias) != cloudProviderAlias {
			continue
		}
		if strings.TrimSpace(context.KubernetesContext) == kubernetesContext || strings.TrimSpace(context.Name) == kubernetesContext {
			status.CloudContextConfig = context
			return status, true, nil
		}
	}
	return eruncommon.CloudContextStatus{}, false, nil
}

func (a *App) ensureLinkedCloudContextRunning(config eruncommon.EnvConfig) (eruncommon.CloudContextStatus, bool, error) {
	status, ok, err := a.linkedCloudContext(config)
	if err != nil || !ok {
		return status, ok, err
	}
	if strings.TrimSpace(status.Status) == eruncommon.CloudContextStatusRunning {
		a.emitAppStatus(fmt.Sprintf("Cloud context %s is running. Opening environment...", cloudContextDisplayName(status)), true)
		return status, true, nil
	}
	a.emitAppStatus(fmt.Sprintf("Starting cloud context %s and waiting for Kubernetes access...", cloudContextDisplayName(status)), true)
	status, err = eruncommon.StartCloudContext(eruncommon.Context{}, a.deps.store, eruncommon.CloudContextParams{Name: status.Name}, a.deps.cloudContextDeps)
	if err != nil {
		return eruncommon.CloudContextStatus{}, true, err
	}
	a.emitAppStatus(fmt.Sprintf("Cloud context %s is running. Opening environment...", cloudContextDisplayName(status)), true)
	return status, true, nil
}

func (a *App) stopCloudContext(name string) (eruncommon.CloudContextStatus, error) {
	return eruncommon.StopCloudContext(eruncommon.Context{}, a.deps.store, eruncommon.CloudContextParams{Name: name}, a.deps.cloudContextDeps)
}

func (a *App) emitAppStatus(message string, busy bool) {
	if a.ctx == nil || strings.TrimSpace(message) == "" {
		return
	}
	runtime.EventsEmit(a.ctx, appStatusEvent, appStatusPayload{Message: message, Busy: busy})
}

func cloudContextDisplayName(status eruncommon.CloudContextStatus) string {
	if name := strings.TrimSpace(status.KubernetesContext); name != "" {
		return name
	}
	return strings.TrimSpace(status.Name)
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
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return startSessionResult{}, err
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
		Dir:        resolveTerminalStartDir(result.RepoPath),
		Executable: a.deps.resolveCLIPath(),
		Args:       buildOpenArgs(result.Tenant, result.Environment, selection.Debug),
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
	return a.startCommandSession(selection, cols, rows, initSelectionKey(selection), buildInitArgs(selection), resolveInitStartDir(a.deps.findProjectRoot), []string{appSessionEnvVar + "=1"})
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
	return a.startCommandSession(selection, cols, rows, deploySelectionKey(selection), buildDeployArgs(selection), resolveDeployStartDir(a.deps.findProjectRoot, result), []string{appSessionEnvVar + "=1"})
}

func (a *App) StartCloudInitAWSSession(cols, rows int) (startSessionResult, error) {
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 34
	}
	key := "cloud/init/aws"

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
		Dir:        resolveTerminalStartDir(""),
		Executable: a.deps.resolveCLIPath(),
		Args:       buildCloudInitAWSArgs(),
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
		session: session,
		key:     key,
		serial:  serial,
	}
	a.sessions[key] = managed
	a.current = managed
	a.mu.Unlock()

	go a.streamSession(managed)

	return startSessionResult{SessionID: serial}, nil
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
	envConfig, _, err := store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return deleteEnvironmentResult{}, err
	}
	linkedContext, hasLinkedContext, err := a.ensureLinkedCloudContextRunning(envConfig)
	if err != nil {
		return deleteEnvironmentResult{}, err
	}

	result, err := eruncommon.RunDeleteEnvironment(eruncommon.Context{}, eruncommon.DeleteEnvironmentParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	}, store, a.deps.deleteNamespace)
	stopError := ""
	if hasLinkedContext {
		if _, stopErr := a.stopCloudContext(linkedContext.Name); stopErr != nil {
			stopError = stopErr.Error()
		}
	}
	if err != nil {
		return deleteEnvironmentResult{}, err
	}
	a.closeSessionsForSelection(selection)
	return deleteEnvironmentResult{
		Tenant:                result.Tenant,
		Environment:           result.Environment,
		Namespace:             result.Namespace,
		KubernetesContext:     result.KubernetesContext,
		NamespaceDeleteError:  result.NamespaceDeleteError,
		CloudContextStopError: stopError,
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
	mcpPort := eruncommon.MCPPortForResult(result)
	if a.deps.ensureMCP != nil && !a.deps.canConnectLocalPort(mcpPort) {
		if err := a.deps.ensureMCP(ctx, result); err != nil {
			if !a.deps.canConnectLocalPort(mcpPort) {
				return eruncommon.DiffResult{}, err
			}
		}
	}
	endpoint := mcpEndpointForOpenResult(result)
	diff, err := a.deps.loadDiff(ctx, endpoint)
	if err == nil || a.deps.ensureMCP == nil {
		return diff, err
	}
	if ensureErr := a.deps.ensureMCP(ctx, result); ensureErr != nil {
		return eruncommon.DiffResult{}, err
	}
	return a.deps.loadDiff(ctx, endpoint)
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

func listKubernetesContexts() ([]string, error) {
	output, err := exec.Command("kubectl", "config", "get-contexts", "-o=name").Output()
	if err != nil {
		return nil, err
	}
	contexts := strings.Split(string(output), "\n")

	currentOutput, err := exec.Command("kubectl", "config", "current-context").Output()
	if err == nil {
		contexts = preferCurrentKubernetesContext(contexts, string(currentOutput))
	}

	return contexts, nil
}

func normalizeKubernetesContexts(contexts []string) []string {
	seen := make(map[string]struct{}, len(contexts))
	result := make([]string, 0, len(contexts))
	for _, context := range contexts {
		context = strings.TrimSpace(context)
		if context == "" {
			continue
		}
		if _, ok := seen[context]; ok {
			continue
		}
		seen[context] = struct{}{}
		result = append(result, context)
	}
	return result
}

func preferCurrentKubernetesContext(contexts []string, current string) []string {
	current = strings.TrimSpace(current)
	if current == "" {
		return contexts
	}

	result := make([]string, 0, len(contexts)+1)
	result = append(result, current)
	for _, context := range contexts {
		if strings.TrimSpace(context) == current {
			continue
		}
		result = append(result, context)
	}
	return result
}

func normalizeSelection(selection uiSelection) uiSelection {
	return uiSelection{
		Tenant:            strings.TrimSpace(selection.Tenant),
		Environment:       strings.TrimSpace(selection.Environment),
		Version:           strings.TrimSpace(selection.Version),
		RuntimeImage:      strings.TrimSpace(selection.RuntimeImage),
		KubernetesContext: strings.TrimSpace(selection.KubernetesContext),
		ContainerRegistry: strings.TrimSpace(selection.ContainerRegistry),
		NoGit:             selection.NoGit,
		Bootstrap:         selection.Bootstrap,
		SetDefaultTenant:  selection.SetDefaultTenant,
		Action:            strings.TrimSpace(selection.Action),
		Debug:             selection.Debug,
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
	return selection.Tenant + "\x00" + selection.Environment + "\x00" + fmt.Sprintf("%t", selection.Debug)
}

func initSelectionKey(selection uiSelection) string {
	selection = normalizeSelection(selection)
	return "init\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00" + selection.Version + "\x00" + selection.RuntimeImage + "\x00" + selection.KubernetesContext + "\x00" + selection.ContainerRegistry + "\x00" + fmt.Sprintf("%t", selection.SetDefaultTenant) + "\x00" + fmt.Sprintf("%t", selection.NoGit) + "\x00" + fmt.Sprintf("%t", selection.Bootstrap) + "\x00" + fmt.Sprintf("%t", selection.Debug)
}

func deploySelectionKey(selection uiSelection) string {
	selection = normalizeSelection(selection)
	return "deploy\x00" + selection.Tenant + "\x00" + selection.Environment + "\x00" + selection.Version + "\x00" + selection.RuntimeImage + "\x00" + fmt.Sprintf("%t", selection.Debug)
}
