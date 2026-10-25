package main

import (
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-ui/internal/runtimeview"
	settingssvc "github.com/sophium/erun/erun-ui/internal/settings"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

type uiState struct {
	Tenants                  []uiTenant                        `json:"tenants"`
	Selected                 *uimodel.Selection                `json:"selected,omitempty"`
	Message                  string                            `json:"message,omitempty"`
	Build                    uiBuildDetails                    `json:"build"`
	VersionSuggestions       []uiVersion                       `json:"versionSuggestions,omitempty"`
	VersionSuggestionNotices []uiVersionNotice                 `json:"versionSuggestionNotices,omitempty"`
	CloudProviders           []settingssvc.CloudProviderStatus `json:"cloudProviders,omitempty"`
	// ConfigUnreadable distinguishes a failed config read from no environments.
	ConfigUnreadable bool `json:"configUnreadable,omitempty"`
}

type uiTenant struct {
	Name                      string          `json:"name"`
	DefaultEnvironment        string          `json:"defaultEnvironment,omitempty"`
	CloudProviderAliases      []string        `json:"cloudProviderAliases,omitempty"`
	PrimaryCloudProviderAlias string          `json:"primaryCloudProviderAlias,omitempty"`
	Environments              []uiEnvironment `json:"environments"`
}

type uiEnvironment struct {
	Name           string `json:"name"`
	Type           string `json:"type,omitempty"`
	MCPURL         string `json:"mcpUrl,omitempty"`
	APIURL         string `json:"apiUrl,omitempty"`
	RuntimeVersion string `json:"runtimeVersion,omitempty"`
	// These mirror lifecycle.ListEnvironmentResult's fields of the same name.
	RuntimeVersionLine       *model.RuntimeVersionLine             `json:"runtimeVersionLine,omitempty"`
	ErunVersion              *model.ErunVersion                    `json:"erunVersion,omitempty"`
	RuntimeImageLineMismatch *model.RuntimeImageLineMismatchResult `json:"runtimeImageLineMismatch,omitempty"`
	KubernetesContext        string                                `json:"kubernetesContext,omitempty"`
	IsActive                 bool                                  `json:"isActive,omitempty"`
	SSHDEnabled              bool                                  `json:"sshdEnabled,omitempty"`
	AutoStart                *bool                                 `json:"autoStart,omitempty"`
	// Activity seeds a frontend store reset; the poller only emits on transitions.
	// nil means not yet observed.
	Activity *runtimeview.ActivitySnapshot `json:"activity,omitempty"`
	// Usage is the usage poller's last reading; nil means not yet observed.
	Usage *runtimeview.UsageSnapshot `json:"usage,omitempty"`
	// Node nil means erun manages no node for this env, never a failed read.
	Node *runtimeview.NodeSnapshot `json:"node,omitempty"`
}

type uiBuildDetails struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
}

type uiVersion = registry.RuntimeVersionSuggestion

// uiVersionNotice explains empty suggestions; Kind is "auth" or "unreachable".
type uiVersionNotice struct {
	Image string `json:"image"`
	Kind  string `json:"kind"`
}

// uiVersionSuggestions is the version picker's read model: the deployable
// version choices plus per-source notices for images that could not be listed.
type uiVersionSuggestions struct {
	Suggestions []uiVersion       `json:"suggestions"`
	Notices     []uiVersionNotice `json:"notices,omitempty"`
}

// orchestratorShellActivityPayload is emitted every tick so a missed event
// self-heals.
type orchestratorShellActivityPayload struct {
	SessionID     int    `json:"sessionId"`
	Running       bool   `json:"running"`
	Command       string `json:"command,omitempty"`
	StartedAtUnix int64  `json:"startedAtUnix,omitempty"`
}
