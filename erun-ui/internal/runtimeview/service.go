package runtimeview

import (
	"context"
	"strings"
	"sync"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

type Deps struct {
	Store seam.ListStore
	// Context is read per call: the Wails context arrives after the service is built.
	Context func() context.Context
	Emit    uimodel.Emitter

	// ExecRuntimePod runs a script in the runtime pod over kubectl exec, so pod
	// reads still work while the MCP edge is down.
	ExecRuntimePod       func(ctx context.Context, selection uimodel.Selection, script string) (string, error)
	LoadUsage            func(ctx context.Context, selection uimodel.Selection) (Usage, error)
	LoadResourceStatus   func(ctx context.Context, input ResourceInput) (ResourceStatus, error)
	LoadClusterRegistry  func(ctx context.Context, input ResourceInput) (ClusterRegistryStatus, error)
	LoadHostedRegistry   func(ctx context.Context) HostedRegistryStatus
	CheckRuntimeDeployed func(ctx context.Context, kubeContext, tenant, environment string) (bool, error)
	ResolveImageRegistry func(ctx context.Context, namespace, repository string) (model.RuntimeRegistryVersions, error)
	// ChartAvailabilityOverride pins which charts a version has, for the Playwright harness.
	ChartAvailabilityOverride func(version string) (map[string]bool, bool)

	CanConnectLocalPort func(port int) bool
	CanReachMCPEndpoint func(port int) bool
	MCPEndpoint         func(config.OpenResult) string
	MCPBearer           func(tenant, environment string) string
	LoadIdleStatus      func(ctx context.Context, endpoint, bearer string) (model.EnvironmentIdleStatus, error)
	LoadPodBranch       func(ctx context.Context, endpoint, bearer string) (string, error)
	// RunCommand runs the host-side git and gh lookups for the working issue.
	RunCommand CommandRunner

	// The activity sweep is what notices a forward going bad, so it drives the
	// forward repair and the orchestrator edge bookkeeping that live elsewhere.
	ForgetForwardRepair    func(selection uimodel.Selection)
	ReconcileForwardHealth func(selection uimodel.Selection, port int, portIsBound bool) (outage bool)
	NoteEdgeAnswering      func(selection uimodel.Selection)

	EffectiveContainerRegistries func(tenant, environment string, env config.EnvConfig) (config.ContainerRegistries, bool)
	LinkedCloudContextFor        func(cloudProviderAlias, kubernetesContext string) (model.CloudContextStatus, bool, error)
	RuntimeRegistryNamespace     func(tenant, environment string) string

	// UsageHistoryPath persists the usage cache across restarts; "" keeps it in memory.
	UsageHistoryPath string
}

type Service struct {
	d Deps

	// mu guards activity and usage; it is a leaf.
	mu       sync.Mutex
	activity map[string]ActivityState
	usage    map[string]UsageReading

	nodesMu sync.Mutex
	nodes   map[string]NodeSnapshot

	workingIssueMu    sync.Mutex
	workingIssueCache map[string]workingIssueCacheEntry
}

// New panics on a missing seam rather than defaulting it, so a test can never
// reach a real process or network call it did not wire. It seeds the usage
// cache from UsageHistoryPath.
func New(d Deps) *Service {
	if missing := missingSeams(d); len(missing) > 0 {
		panic("runtimeview.New: missing " + strings.Join(missing, ", "))
	}
	return &Service{
		d:                 d,
		usage:             loadPersistedEnvironmentUsage(d.UsageHistoryPath),
		workingIssueCache: make(map[string]workingIssueCacheEntry),
	}
}

func missingSeams(d Deps) []string {
	required := []struct {
		name    string
		missing bool
	}{
		{"Store", d.Store == nil},
		{"Context", d.Context == nil},
		{"Emit", d.Emit == nil},
		{"ExecRuntimePod", d.ExecRuntimePod == nil},
		{"LoadUsage", d.LoadUsage == nil},
		{"LoadResourceStatus", d.LoadResourceStatus == nil},
		{"LoadClusterRegistry", d.LoadClusterRegistry == nil},
		{"LoadHostedRegistry", d.LoadHostedRegistry == nil},
		{"CheckRuntimeDeployed", d.CheckRuntimeDeployed == nil},
		{"ResolveImageRegistry", d.ResolveImageRegistry == nil},
		{"ChartAvailabilityOverride", d.ChartAvailabilityOverride == nil},
		{"CanConnectLocalPort", d.CanConnectLocalPort == nil},
		{"CanReachMCPEndpoint", d.CanReachMCPEndpoint == nil},
		{"MCPEndpoint", d.MCPEndpoint == nil},
		{"MCPBearer", d.MCPBearer == nil},
		{"LoadIdleStatus", d.LoadIdleStatus == nil},
		{"LoadPodBranch", d.LoadPodBranch == nil},
		{"RunCommand", d.RunCommand == nil},
		{"ForgetForwardRepair", d.ForgetForwardRepair == nil},
		{"ReconcileForwardHealth", d.ReconcileForwardHealth == nil},
		{"NoteEdgeAnswering", d.NoteEdgeAnswering == nil},
		{"EffectiveContainerRegistries", d.EffectiveContainerRegistries == nil},
		{"LinkedCloudContextFor", d.LinkedCloudContextFor == nil},
		{"RuntimeRegistryNamespace", d.RuntimeRegistryNamespace == nil},
	}
	var missing []string
	for _, seam := range required {
		if seam.missing {
			missing = append(missing, seam.name)
		}
	}
	return missing
}

// notify posts a toast with no environment identity.
func (s *Service) notify(kind, message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	s.d.Emit(uimodel.AppNotificationEvent, uimodel.AppNotificationPayload{Kind: kind, Message: message})
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
