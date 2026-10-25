// Package activity owns the desktop's activity queue: the long-running commands
// it tracks from helm releases, PTY trace lines and its own per-environment
// action runner, the terminal locks a deploy holds, the stale-shell detector,
// deploy orchestration and components, version pinning, and the environment
// trace.
package activity

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	seampkg "github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Store is the slice of the desktop's config store the activity concern reads.
type Store interface {
	seampkg.ListStore
}

// Terminal session kinds, as the terminal names them; the locks, the trace and
// the stale-shell detector tell sessions apart by them.
const (
	KindOpen           = "erun"
	KindLocal          = "local"
	KindAI             = "ai"
	KindCommand        = "command"
	KindOrchestrator   = "orchestrator"
	KindContributeERun = "contribute-erun"
	KindContributeAI   = "contribute-ai"
)

// Session is the activity concern's view of one live terminal session.
type Session struct {
	Serial    int
	Selection uimodel.Selection
	Kind      string
	StartedAt time.Time
	// Pid is set only on the sessions DeadSessions reports.
	Pid int
}

// Sessions is the terminal session registry as the activity concern reaches it.
// Each call takes the terminal lock itself, so none may be made while an
// activity lock is held.
type Sessions interface {
	// LockMatching locks every open session match accepts and id does not
	// already lock, and returns the sessions it locked.
	LockMatching(id string, match func(Session) bool) []Session
	// LockSerial locks the open session with serial to id; false when there is
	// none or id already locks it.
	LockSerial(serial int, id string) (Session, bool)
	// Unlock releases every session id locks and returns them.
	Unlock(id string) []Session
	// DeadSessions lists open sessions older than minAge whose process has died
	// while their PTY stayed open.
	DeadSessions(minAge time.Duration) []Session
	// Kill marks the session with serial closed and closes its PTY; false when there is none.
	Kill(serial int) bool
	// OpenSelections is the selection of every open session.
	OpenSelections() []uimodel.Selection
}

type Deps struct {
	Store           Store
	FindProjectRoot seampkg.ProjectFinderFunc
	// Context is read per call: the Wails context arrives after the service is built.
	Context func() context.Context
	// Started reports whether startup has run. Before it (and in unit tests) a
	// deploy never falls back to the real CLI and config.
	Started  func() bool
	Emit     uimodel.Emitter
	Sessions Sessions
	// AppSessionEnv marks every process the desktop launches as its own ("NAME=1").
	AppSessionEnv string

	ResolveCLIPath           func() string
	ResolveImageRegistry     func(ctx context.Context, namespace, repository string) (model.RuntimeRegistryVersions, error)
	RuntimeRegistryNamespace func(tenant, environment string) string
	RuntimeChartPlan         func(tenant, environment, version string, runtimeIsLocalChart bool) uimodel.ChartPlan
	// FetchContainerStatuses errors on a kubectl outage so the frontend keeps the last-known state.
	FetchContainerStatuses func(ctx context.Context, entry Entry) ([]ContainerStatus, error)

	MCPEndpoint func(config.OpenResult) string
	MCPBearer   func(tenant, environment string) string
	// CanReachMCPEndpoint checks traffic, not a dial: a stale port-forward accepts and never answers.
	CanReachMCPEndpoint func(port int) bool
	RunPodRaw           func(ctx context.Context, endpoint, bearer string, command []string) (string, error)

	StartDir                   func(repoPath string) string
	AppendDeployComponents     func(args []string, selection uimodel.Selection) []string
	RuntimeImageOverride       func(selection uimodel.Selection) string
	AppendMCPAuthPublicKeyFlag func(args []string) []string

	EnvironmentDeployed    func(tenant, environment string)
	EnvironmentInitialized func(tenant, environment string)
	EnvironmentInitFailed  func(tenant, environment string)
	DoctorCompleted        func(tenant, environment string, success bool, message string)
	SSHDInitCompleted      func(tenant, environment string, success bool, message string)

	// InterruptedActivityPath is read per call so a test can point it elsewhere after construction.
	InterruptedActivityPath func() string
}

type Service struct {
	d     Deps
	queue *queueStore

	// pollersMu guards pollersStop; it is a leaf.
	pollersMu   sync.Mutex
	pollersStop chan struct{}

	watchedContextsMu sync.Mutex
	watchedContexts   map[string]struct{}

	actionQueueMu sync.Mutex
	actionQueues  map[string]*envActionQueue
	actionCancels map[string]context.CancelFunc
}

// New panics on a missing seam rather than defaulting it, so a test can never
// reach a real process or network call it did not wire.
func New(d Deps) *Service {
	if missing := missingSeams(d); len(missing) > 0 {
		panic("activity.New: missing " + strings.Join(missing, ", "))
	}
	s := &Service{d: d}
	s.queue = newQueueStore(s.emitState, nil)
	return s
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
		{"Sessions", d.Sessions == nil},
		{"AppSessionEnv", d.AppSessionEnv == ""},
		{"ResolveCLIPath", d.ResolveCLIPath == nil},
		{"ResolveImageRegistry", d.ResolveImageRegistry == nil},
		{"RuntimeRegistryNamespace", d.RuntimeRegistryNamespace == nil},
		{"RuntimeChartPlan", d.RuntimeChartPlan == nil},
		{"FetchContainerStatuses", d.FetchContainerStatuses == nil},
		{"MCPEndpoint", d.MCPEndpoint == nil},
		{"MCPBearer", d.MCPBearer == nil},
		{"CanReachMCPEndpoint", d.CanReachMCPEndpoint == nil},
		{"RunPodRaw", d.RunPodRaw == nil},
		{"StartDir", d.StartDir == nil},
		{"AppendDeployComponents", d.AppendDeployComponents == nil},
		{"RuntimeImageOverride", d.RuntimeImageOverride == nil},
		{"AppendMCPAuthPublicKeyFlag", d.AppendMCPAuthPublicKeyFlag == nil},
		{"EnvironmentDeployed", d.EnvironmentDeployed == nil},
		{"EnvironmentInitialized", d.EnvironmentInitialized == nil},
		{"EnvironmentInitFailed", d.EnvironmentInitFailed == nil},
		{"DoctorCompleted", d.DoctorCompleted == nil},
		{"SSHDInitCompleted", d.SSHDInitCompleted == nil},
		{"InterruptedActivityPath", d.InterruptedActivityPath == nil},
	}
	var missing []string
	for _, seam := range required {
		if seam.missing {
			missing = append(missing, seam.name)
		}
	}
	return missing
}

// Track registers entry as observed activity; an entry already tracked under
// its ID is returned unchanged with false.
func (s *Service) Track(entry Entry) (Entry, bool) {
	return s.queue.start(entry)
}

func (s *Service) emitEvent(name string, payload any) {
	s.d.Emit(name, payload)
}

// emitEnvNotification posts an env-scoped notification a later clear can dismiss.
func (s *Service) emitEnvNotification(kind, tenant, environment, source, message, action string) {
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

// emitClearEnvNotification dismisses an env notification matching source, tenant, and environment.
func (s *Service) emitClearEnvNotification(tenant, environment, source string) {
	s.d.Emit(uimodel.AppNotificationClearEvent, uimodel.AppNotificationClearPayload{
		Tenant:      tenant,
		Environment: environment,
		Source:      source,
	})
}

// emitEnvStatus publishes the env's condition for the sidebar row; "" clears.
func (s *Service) emitEnvStatus(selection uimodel.Selection, status string) {
	s.emitEvent(uimodel.EnvStatusEvent, uimodel.EnvStatusPayload{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
		Status:      status,
	})
}
