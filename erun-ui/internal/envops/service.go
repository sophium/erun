// Package envops owns the per-environment operations the desktop runs against an
// environment it does not have a terminal open in: the runtime reconnect and its
// forward repair, stop and run state, workspace sync, jobs, exposure, outputs,
// exec commit and push, the diff review chip, and host-side workspace actions.
package envops

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/envstore"
	"github.com/sophium/erun/erun-common/base/seam/projectroot"
	"github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/command/lifecycle/stop"
	"github.com/sophium/erun/erun-common/command/workspace"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Store is the slice of the desktop's config store the operations read and write.
type Store interface {
	envstore.ListStore
	SaveEnvConfig(string, config.EnvConfig) error
}

// ReviewPolicy names what the diff review chip checks on the tenant's platform;
// main passes the tenant service's names so the two never drift.
type ReviewPolicy struct {
	ReadReviews       string
	ReadComments      string
	ReadMergeQueue    string
	AdvanceMergeQueue string
	// NoPermissionState is the platform state reported when reviews are not readable.
	NoPermissionState string
	RequestTimeout    time.Duration
}

type Deps struct {
	Store           Store
	Cloud           cloud.CloudDependencies
	FindProjectRoot projectroot.ProjectFinderFunc
	// Context is read per call: the Wails context arrives after the service is built.
	Context func() context.Context
	// Started reports whether startup has run. Before it (and in unit tests) the
	// runtime reconnect never starts, so nothing falls back to the real CLI.
	Started func() bool
	Emit    uimodel.Emitter

	MCPEndpoint         func(config.OpenResult) string
	MCPBearer           func(tenant, environment string) string
	CanConnectLocalPort func(port int) bool
	// CanReachMCPEndpoint checks traffic, not a dial: a stale port-forward accepts and never answers.
	CanReachMCPEndpoint func(port int) bool
	WrapMCPUnreachable  func(kind mcp.LocalMCPUnreachableKind, err error) error
	IsMCPDialFailure    func(err error) bool

	ReconnectMCP func(ctx context.Context, result config.OpenResult, onLine func(string)) error
	EnsureSSHD   func(ctx context.Context, result config.OpenResult) error
	// ReconnectLineHandler feeds the reconnect's trace lines to the activity queue.
	ReconnectLineHandler func(selection uimodel.Selection) func(string)
	// DeployInFlight reports a deploy holding the environment's terminals; it is the recovery already under way.
	DeployInFlight    func(selection uimodel.Selection) bool
	NoteEdgeAnswering func(selection uimodel.Selection)

	StopRuntime          func(core.Context, stop.StopEnvironmentParams) (model.StopEnvironmentResult, error)
	ReadRuntimeRunState  func(core.Context, stop.RuntimeScaleTarget) (stop.RuntimeRunState, error)
	ResolveImageRegistry func(ctx context.Context, namespace, repository string) (model.RuntimeRegistryVersions, error)

	LoadJobs      func(ctx context.Context, endpoint, bearer string) ([]jobstate.EnvironmentJob, error)
	ReadJobOutput func(ctx context.Context, endpoint, bearer string, params model.ReadEnvironmentJobOutputParams) (jobs.EnvironmentJobOutput, error)
	CancelJob     func(ctx context.Context, endpoint, bearer string, params model.CancelEnvironmentJobParams) (jobs.CancelEnvironmentJobResult, error)
	ExecCommit    func(ctx context.Context, endpoint, bearer, branch, message string) (model.CommitWorkingTreeResult, error)
	ExecPush      func(ctx context.Context, endpoint, bearer, branch, remote string) (model.PushWorkingTreeBranchResult, error)

	ListAgentOutputs    func(config.OpenResult, model.RuntimeOutputsParams) (model.RuntimeOutputsListResult, error)
	DownloadAgentOutput func(config.OpenResult, model.RuntimeOutputDownloadParams) (model.RuntimeOutputResult, error)
	LaunchHostOpener    func(executable string, args []string) error
	LaunchHostArtifact  func(exePath, dir string) error
	// OpenHostIDE launches ide on a host path; the terminal side owns the IDE command line.
	OpenHostIDE func(ide, path string) error

	WorkspaceSyncReady    func(ctx context.Context, hostAlias string) error
	SyncWorkspace         func(ctx context.Context, params workspace.WorkspaceSyncParams) (model.WorkspaceSyncResult, error)
	WorkspaceSyncInterval time.Duration

	// The diff review chip reads the tenant's platform through the tenant service;
	// ReviewPlatform's state is "" when the platform is ready.
	ReviewPlatform func(tenant string) (state string, client *platformclient.PlatformClient, err error)
	RestrictedRead func(capabilities model.PlatformCapabilities, capability string) string
	QueuePosition  func(queue []model.PlatformReview, reviewID string) int
	Review         ReviewPolicy
}

type Service struct {
	d Deps

	// mu guards workspaceSyncs, forwardRepairs and runtimeStops; it is a leaf.
	mu             sync.Mutex
	workspaceSyncs map[string]*workspaceSyncWorker
	forwardRepairs map[string]forwardRepairEpisode
	// runtimeStops is separate from the terminal's intentional stops because their recoveries differ.
	runtimeStops map[string]struct{}

	ensureMu           sync.Mutex
	ensureInflight     map[string]*ensureRun
	ensureDone         map[string]time.Time
	ensureFailNotified map[string]struct{}

	// initEmitted fires environment-initialized once per env: a Windows ConPTY
	// repaint re-sends "==> Initialized", which would loop create→deploy.
	initEmittedMu sync.Mutex
	initEmitted   map[string]struct{}
}

// New panics on a missing seam rather than defaulting it, so a test can never
// reach a real process or network call it did not wire.
func New(d Deps) *Service {
	if missing := missingSeams(d); len(missing) > 0 {
		panic("envops.New: missing " + strings.Join(missing, ", "))
	}
	return &Service{
		d:                  d,
		workspaceSyncs:     make(map[string]*workspaceSyncWorker),
		forwardRepairs:     make(map[string]forwardRepairEpisode),
		runtimeStops:       make(map[string]struct{}),
		ensureInflight:     make(map[string]*ensureRun),
		ensureDone:         make(map[string]time.Time),
		ensureFailNotified: make(map[string]struct{}),
		initEmitted:        make(map[string]struct{}),
	}
}

func missingSeams(d Deps) []string {
	required := []struct {
		name    string
		missing bool
	}{
		{"Store", d.Store == nil},
		{"FindProjectRoot", d.FindProjectRoot == nil},
		{"Context", d.Context == nil},
		{"Started", d.Started == nil},
		{"Emit", d.Emit == nil},
		{"MCPEndpoint", d.MCPEndpoint == nil},
		{"MCPBearer", d.MCPBearer == nil},
		{"CanConnectLocalPort", d.CanConnectLocalPort == nil},
		{"CanReachMCPEndpoint", d.CanReachMCPEndpoint == nil},
		{"WrapMCPUnreachable", d.WrapMCPUnreachable == nil},
		{"IsMCPDialFailure", d.IsMCPDialFailure == nil},
		{"ReconnectMCP", d.ReconnectMCP == nil},
		{"EnsureSSHD", d.EnsureSSHD == nil},
		{"ReconnectLineHandler", d.ReconnectLineHandler == nil},
		{"DeployInFlight", d.DeployInFlight == nil},
		{"NoteEdgeAnswering", d.NoteEdgeAnswering == nil},
		{"StopRuntime", d.StopRuntime == nil},
		{"ReadRuntimeRunState", d.ReadRuntimeRunState == nil},
		{"ResolveImageRegistry", d.ResolveImageRegistry == nil},
		{"LoadJobs", d.LoadJobs == nil},
		{"ReadJobOutput", d.ReadJobOutput == nil},
		{"CancelJob", d.CancelJob == nil},
		{"ExecCommit", d.ExecCommit == nil},
		{"ExecPush", d.ExecPush == nil},
		{"ListAgentOutputs", d.ListAgentOutputs == nil},
		{"DownloadAgentOutput", d.DownloadAgentOutput == nil},
		{"LaunchHostOpener", d.LaunchHostOpener == nil},
		{"LaunchHostArtifact", d.LaunchHostArtifact == nil},
		{"OpenHostIDE", d.OpenHostIDE == nil},
		{"WorkspaceSyncReady", d.WorkspaceSyncReady == nil},
		{"SyncWorkspace", d.SyncWorkspace == nil},
		{"WorkspaceSyncInterval", d.WorkspaceSyncInterval <= 0},
		{"ReviewPlatform", d.ReviewPlatform == nil},
		{"RestrictedRead", d.RestrictedRead == nil},
		{"QueuePosition", d.QueuePosition == nil},
		{"Review.RequestTimeout", d.Review.RequestTimeout <= 0},
	}
	var missing []string
	for _, seam := range required {
		if seam.missing {
			missing = append(missing, seam.name)
		}
	}
	return missing
}

// Notification sources let a later state change dismiss the notification; the
// frontend matches them by value.
const (
	// SourceRuntimeUnreachable must match dismissNotificationForEnv in the frontend.
	SourceRuntimeUnreachable = "runtime-unreachable"
	// SourceForwardOutage tags a port-forward repair that ran and did not help,
	// unlike SourceRuntimeUnreachable.
	SourceForwardOutage       = "port-forward-outage"
	sourceWorkspaceSyncNoPath = "workspace-sync-no-path"
	sourceWorkspaceSyncFailed = "workspace-sync-failed"
)

// notify posts a toast with no environment identity.
func (s *Service) notify(kind, message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	s.d.Emit(uimodel.AppNotificationEvent, uimodel.AppNotificationPayload{Kind: kind, Message: message})
}

// notifyEnv posts an env-scoped notification clearEnvNotification can dismiss.
func (s *Service) notifyEnv(kind, tenant, environment, source, message, action string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	s.d.Emit(uimodel.AppNotificationEvent, uimodel.AppNotificationPayload{
		Kind:        kind,
		Message:     message,
		Tenant:      tenant,
		Environment: environment,
		Source:      source,
		Action:      action,
	})
}

func (s *Service) clearEnvNotification(tenant, environment, source string) {
	s.d.Emit(uimodel.AppNotificationClearEvent, uimodel.AppNotificationClearPayload{
		Tenant:      tenant,
		Environment: environment,
		Source:      source,
	})
}

// emitEnvStatus publishes the env's condition for the sidebar row; "" clears.
func (s *Service) emitEnvStatus(selection uimodel.Selection, status string) {
	s.d.Emit(uimodel.EnvStatusEvent, uimodel.EnvStatusPayload{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
		Status:      status,
	})
}

// classifyMCPUnreachable reports the locally observable kind of unreachability.
func (s *Service) classifyMCPUnreachable(port int) mcp.LocalMCPUnreachableKind {
	if s.d.CanConnectLocalPort(port) {
		return mcp.LocalMCPStaleForward
	}
	return mcp.LocalMCPNotOpen
}
