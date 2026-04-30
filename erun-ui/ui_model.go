package main

import eruncommon "github.com/sophium/erun/erun-common"

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
	IsActive       bool   `json:"isActive,omitempty"`
	SSHDEnabled    bool   `json:"sshdEnabled,omitempty"`
}

type uiSelection struct {
	Tenant            string `json:"tenant"`
	Environment       string `json:"environment"`
	Version           string `json:"version,omitempty"`
	RuntimeImage      string `json:"runtimeImage,omitempty"`
	RuntimeCPU        string `json:"runtimeCpu,omitempty"`
	RuntimeMemory     string `json:"runtimeMemory,omitempty"`
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
	Name                 string                  `json:"name"`
	RepoPath             string                  `json:"repoPath"`
	KubernetesContext    string                  `json:"kubernetesContext"`
	ContainerRegistry    string                  `json:"containerRegistry"`
	CloudProviderAlias   string                  `json:"cloudProviderAlias"`
	CloudProviderAliases []string                `json:"cloudProviderAliases,omitempty"`
	CloudContext         *uiCloudContextStatus   `json:"cloudContext,omitempty"`
	RuntimeVersion       string                  `json:"runtimeVersion"`
	RuntimePod           uiRuntimePodConfig      `json:"runtimePod"`
	SSHD                 uiSSHDConfig            `json:"sshd"`
	Idle                 uiIdleConfig            `json:"idle"`
	LocalPorts           uiEnvironmentLocalPorts `json:"localPorts"`
	Remote               bool                    `json:"remote"`
	Snapshot             bool                    `json:"snapshot"`
}

type uiRuntimePodConfig struct {
	CPU    string `json:"cpu"`
	Memory string `json:"memory"`
}

type uiRuntimeResourceInput struct {
	KubernetesContext string `json:"kubernetesContext"`
	Tenant            string `json:"tenant,omitempty"`
	Environment       string `json:"environment,omitempty"`
}

type uiRuntimeResourceStatus struct {
	KubernetesContext string                  `json:"kubernetesContext"`
	Available         bool                    `json:"available"`
	Message           string                  `json:"message,omitempty"`
	CPU               uiRuntimeResourceMetric `json:"cpu"`
	Memory            uiRuntimeResourceMetric `json:"memory"`
	Nodes             []uiRuntimeResourceNode `json:"nodes,omitempty"`
}

type uiRuntimeResourceMetric struct {
	Total     float64 `json:"total"`
	Used      float64 `json:"used"`
	Free      float64 `json:"free"`
	Unit      string  `json:"unit"`
	Formatted string  `json:"formatted"`
}

type uiRuntimeResourceNode struct {
	Name   string                  `json:"name"`
	CPU    uiRuntimeResourceMetric `json:"cpu"`
	Memory uiRuntimeResourceMetric `json:"memory"`
}

type uiIdleConfig struct {
	Timeout          string `json:"timeout"`
	WorkingHours     string `json:"workingHours"`
	IdleTrafficBytes int64  `json:"idleTrafficBytes"`
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

type uiIdleStatus struct {
	TimeoutSeconds      int64          `json:"timeoutSeconds"`
	SecondsUntilStop    int64          `json:"secondsUntilStop"`
	StopEligible        bool           `json:"stopEligible"`
	OutsideWorkingHours bool           `json:"outsideWorkingHours"`
	ManagedCloud        bool           `json:"managedCloud"`
	StopBlockedReason   string         `json:"stopBlockedReason,omitempty"`
	StopError           string         `json:"stopError,omitempty"`
	CloudContextName    string         `json:"cloudContextName,omitempty"`
	CloudContextStatus  string         `json:"cloudContextStatus,omitempty"`
	CloudContextLabel   string         `json:"cloudContextLabel,omitempty"`
	Markers             []uiIdleMarker `json:"markers,omitempty"`
}

type uiIdleMarker struct {
	Name             string `json:"name"`
	Idle             bool   `json:"idle"`
	Reason           string `json:"reason,omitempty"`
	SecondsRemaining int64  `json:"secondsRemaining,omitempty"`
}
