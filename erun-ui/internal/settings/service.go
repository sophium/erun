// Package settings owns the desktop's configuration surface: the machine-wide
// config with its cloud providers, cloud contexts and gateway catalog, tenant
// and environment settings, and the config watcher that uploads a hosted
// environment's portable settings when they change.
package settings

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Store is the slice of the desktop's config store the settings read and write.
type Store interface {
	seam.ListStore
	SaveERunConfig(config.ERunConfig) error
	SaveTenantConfig(model.TenantConfig) error
	SaveEnvConfig(string, config.EnvConfig) error
}

// CloudStatusCache is the live cloud-context power state the settings read and seed.
type CloudStatusCache interface {
	ApplyStatuses([]model.CloudContextStatus)
	SetStatus(name, status string)
	Status(name string) string
	ReconcileRefresher(selection uimodel.Selection, enabled bool)
}

type Deps struct {
	Store       Store
	Cloud       cloud.CloudDependencies
	ContextDeps cloud.CloudContextDependencies
	// Context is read per call: the Wails context arrives after the service is built.
	Context       func() context.Context
	Emit          uimodel.Emitter
	CloudStatuses CloudStatusCache

	// The stop bookkeeping a cloud-context stop or start updates lives with the
	// sessions that react to it.
	StopCloudContext     func(context.Context, string) (model.CloudContextStatus, error)
	MarkIntentionalStop  func(cloudContext string)
	ClearIntentionalStop func(cloudContext string)
	ClearIdleStops       func(cloudContext string)
	RecordManualStop     func(ctx context.Context, cloudContext string)

	// A remote worktree's cloud alias is set in its pod, over its MCP edge.
	EnsureMCPAvailable  func(context.Context, config.OpenResult) error
	MCPEndpoint         func(config.OpenResult) string
	MCPBearer           func(tenant, environment string) string
	SetRemoteCloudAlias func(ctx context.Context, endpoint, bearer, tenant, environment, alias string) (config.EnvConfig, error)

	ReconcileWorkspaceSync func(selection uimodel.Selection, enabled bool)
	WorkspaceSyncStatus    func(selection uimodel.Selection) (status, message string)

	// PlatformReady is checked before an automatic upload so an unsigned machine
	// is not notified on every config write.
	PlatformReady          func(tenant string) bool
	PlatformClient         func(ctx context.Context, tenant string) (*platformclient.PlatformClient, context.Context, context.CancelFunc, error)
	PlatformRequestTimeout time.Duration

	ReadGatewayRouting func(ctx context.Context, env config.EnvConfig, tenant string, catalog *config.OpenRouterConfig) (lifecycle.GatewayRouting, bool, error)
}

type Service struct {
	d Deps

	// mu guards configWatcher; it is a leaf.
	mu            sync.Mutex
	configWatcher *configWatcher
	// definitionWriteOrigin keeps the config watcher from firing on this desktop's own writes.
	definitionWriteOrigin definitionWriteOrigin
}

// New panics on a missing seam rather than defaulting it, so a test can never
// reach a real process or network call it did not wire.
func New(d Deps) *Service {
	if missing := missingSeams(d); len(missing) > 0 {
		panic("settings.New: missing " + strings.Join(missing, ", "))
	}
	return &Service{d: d}
}

func missingSeams(d Deps) []string {
	required := []struct {
		name    string
		missing bool
	}{
		{"Store", d.Store == nil},
		{"Context", d.Context == nil},
		{"Emit", d.Emit == nil},
		{"CloudStatuses", d.CloudStatuses == nil},
		{"StopCloudContext", d.StopCloudContext == nil},
		{"MarkIntentionalStop", d.MarkIntentionalStop == nil},
		{"ClearIntentionalStop", d.ClearIntentionalStop == nil},
		{"ClearIdleStops", d.ClearIdleStops == nil},
		{"RecordManualStop", d.RecordManualStop == nil},
		{"EnsureMCPAvailable", d.EnsureMCPAvailable == nil},
		{"MCPEndpoint", d.MCPEndpoint == nil},
		{"MCPBearer", d.MCPBearer == nil},
		{"SetRemoteCloudAlias", d.SetRemoteCloudAlias == nil},
		{"ReconcileWorkspaceSync", d.ReconcileWorkspaceSync == nil},
		{"WorkspaceSyncStatus", d.WorkspaceSyncStatus == nil},
		{"PlatformReady", d.PlatformReady == nil},
		{"PlatformClient", d.PlatformClient == nil},
		{"PlatformRequestTimeout", d.PlatformRequestTimeout <= 0},
		{"ReadGatewayRouting", d.ReadGatewayRouting == nil},
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
