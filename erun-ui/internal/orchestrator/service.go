// Package orchestrator owns the desktop's orchestrators: their definitions and
// linked environments, the background agent and PTY each one runs, the shared
// workspace and hooks, activity and shell reports, pacing nudges, and the whip
// across orchestrators and environments.
package orchestrator

import (
	"context"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	seampkg "github.com/sophium/erun/erun-common/base/seam"
	orchcmd "github.com/sophium/erun/erun-common/command/orchestrator"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Store is the slice of the desktop's config store the orchestrator concern reads and writes.
type Store interface {
	seampkg.ListStore
	SaveERunConfig(config.ERunConfig) error
	SaveEnvConfig(string, config.EnvConfig) error
}

// PTY is an orchestrator's terminal process. Its method set is the terminal
// registry's PTY, so a value of either converts to the other.
type PTY interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
	Wait() error
	Pid() int
	Alive() bool
}

// StartParams is one orchestrator PTY to start.
type StartParams struct {
	Dir        string
	Executable string
	Args       []string
	Env        []string
	Cols       int
	Rows       int
}

// Terminals is the terminal session registry as the orchestrator concern reaches it.
type Terminals interface {
	// WithLock runs fn under the terminal lock. That lock is outermost: this
	// service takes its own lock inside it, never the other way round.
	WithLock(fn func(Registry))
}

// Registry is the session registry inside Terminals.WithLock; it must not
// escape the callback.
type Registry interface {
	// Live returns orchestrator id's session when it is registered and open.
	Live(id string) (Terminal, bool)
	// Register adopts pty as id's session under a fresh serial. respawn is nil
	// for a session that must not be reattached after a crash.
	Register(id string, pty PTY, respawn func() (PTY, error)) Terminal
	// Remove forgets id's session, open or not.
	Remove(id string) (Terminal, bool)
	// TypedRecently reports whether the operator typed into id's session moments ago.
	TypedRecently(id string) bool
}

// Terminal is one registered orchestrator session. Its methods take the
// terminal lock themselves, so none may be called inside Terminals.WithLock.
type Terminal interface {
	Serial() int
	// Stream starts reading a session Register just adopted.
	Stream()
	Close()
	// WriteIfOpen writes text unless the session has closed.
	WriteIfOpen(text string) bool
}

// EnvActivity is one environment's last activity observation.
type EnvActivity struct {
	Snapshot uimodel.ActivitySnapshot
	// BusyHolderOrchestrators is the comma-joined ids holding the environment's busy leases.
	BusyHolderOrchestrators string
}

type Deps struct {
	Store Store
	// Context is read per call: the Wails context arrives after the service is built.
	Context func() context.Context
	// Started reports whether startup has run; the native Save dialog needs it.
	Started   func() bool
	Emit      uimodel.Emitter
	Terminals Terminals
	// ClaudeAgents is the only path to the claude agent lifecycle commands.
	ClaudeAgents  func() AgentCLI
	ResolveLaunch func(launch orchcmd.OrchestratorLaunch, agentID, initialPrompt, mcpConfigPath string) (string, []string, error)
	StartTerminal func(StartParams) (PTY, error)
	// OpenIDE opens path in ide from the shared workspace dir.
	OpenIDE                  func(ctx context.Context, dir, ide, path string) error
	ResolveGatewayCredential func(*config.OpenRouterConfig) (string, error)
	ResolveOpenRouterConfig  func() *config.OpenRouterConfig
	// RunLabelCommand is the gh boundary claim-label provisioning shells out through.
	RunLabelCommand func(ctx context.Context, dir, name string, args ...string) (string, error)
	// CanReachMCPEndpoint checks traffic, not a dial: a stale port-forward accepts and never answers.
	CanReachMCPEndpoint func(port int) bool
	// MCPForwardStale reports a forward that holds the port but no longer reaches the pod.
	MCPForwardStale func(port int) bool
	// MCPEdgeAnswers is the edge probe the shared runtime ensure trusts.
	MCPEdgeAnswers            func(port int) bool
	MCPEndpoint               func(config.OpenResult) string
	MCPBearer                 func(tenant, environment string) string
	WhipEnvironment           func(ctx context.Context, tenant, environment, endpoint, bearer string) (model.WhipResult, error)
	LaunchHostArtifact        func(exePath, dir string) error
	ReportHostArtifactSigning func(model.HostArtifactSigning)
	// EnsureRuntimeOnce opens a linked environment's forwards the way a tab open does.
	EnsureRuntimeOnce func(uimodel.Selection)
	// RepairEdge waits up to budget on the shared ensure for an edge a probe found dead.
	RepairEdge func(selection uimodel.Selection, budget time.Duration)
	// ActivitySnapshot and UsageSnapshot are keyed by uimodel.SelectionKey and
	// read outside every lock; the cards only render them.
	ActivitySnapshot func() map[string]EnvActivity
	UsageSnapshot    func() map[string]uimodel.UsageSnapshot
	// EmitAIActivity publishes an orchestrator pane's busy state the way the terminal does an AI tab's.
	EmitAIActivity func(serial int, busy bool)
	// SkillsSource and AgentsSource are the build's stamped erun-skills/skills
	// and erun-skills/agents paths, "" when the build stamped none.
	SkillsSource func() string
	AgentsSource func() string
	// OpenPath and NudgeHistoryPath are read per call, so a test can point them at its own files.
	OpenPath         func() string
	NudgeHistoryPath func() string
}

type Service struct {
	d Deps

	// mu guards the state below. It is a leaf: a section that also reads
	// sessions takes it inside the terminal lock (withSessions), and nothing
	// that takes another lock, emits or calls back runs while it is held.
	mu            sync.Mutex
	orchestrators map[string]*orchestratorSession
	edgeOutages   map[string]orchestratorEdgeOutage
	// unmanagedPacingReason dedups pacing log lines for orchestrators without a session.
	unmanagedPacingReason map[string]orchestratorPacingReason
	// skillsSourceReported warns once per run about unresolvable shipped skills.
	skillsSourceReported bool
	agentsSourceReported bool
}

// New panics on a missing seam rather than defaulting it, so a test can never
// start a real claude agent or mint a real claim label it did not wire.
func New(d Deps) *Service {
	if missing := missingSeams(d); len(missing) > 0 {
		panic("orchestrator.New: missing " + strings.Join(missing, ", "))
	}
	return &Service{
		d:                     d,
		orchestrators:         make(map[string]*orchestratorSession),
		edgeOutages:           make(map[string]orchestratorEdgeOutage),
		unmanagedPacingReason: make(map[string]orchestratorPacingReason),
	}
}

func missingSeams(d Deps) []string {
	required := []struct {
		name    string
		missing bool
	}{
		{"Store", d.Store == nil},
		{"Context", d.Context == nil},
		{"Started", d.Started == nil},
		{"Emit", d.Emit == nil},
		{"Terminals", d.Terminals == nil},
		{"ClaudeAgents", d.ClaudeAgents == nil},
		{"ResolveLaunch", d.ResolveLaunch == nil},
		{"StartTerminal", d.StartTerminal == nil},
		{"OpenIDE", d.OpenIDE == nil},
		{"ResolveGatewayCredential", d.ResolveGatewayCredential == nil},
		{"ResolveOpenRouterConfig", d.ResolveOpenRouterConfig == nil},
		{"RunLabelCommand", d.RunLabelCommand == nil},
		{"CanReachMCPEndpoint", d.CanReachMCPEndpoint == nil},
		{"MCPForwardStale", d.MCPForwardStale == nil},
		{"MCPEdgeAnswers", d.MCPEdgeAnswers == nil},
		{"MCPEndpoint", d.MCPEndpoint == nil},
		{"MCPBearer", d.MCPBearer == nil},
		{"WhipEnvironment", d.WhipEnvironment == nil},
		{"LaunchHostArtifact", d.LaunchHostArtifact == nil},
		{"ReportHostArtifactSigning", d.ReportHostArtifactSigning == nil},
		{"EnsureRuntimeOnce", d.EnsureRuntimeOnce == nil},
		{"RepairEdge", d.RepairEdge == nil},
		{"ActivitySnapshot", d.ActivitySnapshot == nil},
		{"UsageSnapshot", d.UsageSnapshot == nil},
		{"EmitAIActivity", d.EmitAIActivity == nil},
		{"SkillsSource", d.SkillsSource == nil},
		{"AgentsSource", d.AgentsSource == nil},
		{"OpenPath", d.OpenPath == nil},
		{"NudgeHistoryPath", d.NudgeHistoryPath == nil},
	}
	var missing []string
	for _, seam := range required {
		if seam.missing {
			missing = append(missing, seam.name)
		}
	}
	return missing
}

// withSessions runs fn under the terminal lock and then this service's own,
// the one order a section reading sessions and orchestrator state together takes.
func (s *Service) withSessions(fn func(Registry)) {
	s.d.Terminals.WithLock(func(l Registry) {
		s.mu.Lock()
		defer s.mu.Unlock()
		fn(l)
	})
}

// withState runs fn under this service's own lock alone.
func (s *Service) withState(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn()
}

// OrchestratorRegisteredLocked is the terminal's reconnect check; it runs with
// the terminal lock held, which is the order withSessions takes.
func (s *Service) OrchestratorRegisteredLocked(id string) bool {
	var registered bool
	s.withState(func() { registered = s.orchestrators[id] != nil })
	return registered
}

func (s *Service) emitEvent(name string, payload any) {
	s.d.Emit(name, payload)
}

// emitAppNotification pushes a toast with no env/orchestrator identity.
func (s *Service) emitAppNotification(kind, message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	s.d.Emit(uimodel.AppNotificationEvent, uimodel.AppNotificationPayload{Kind: kind, Message: message})
}

// emitEnvNotification posts an env-scoped notification that a clear can dismiss
// by (source, tenant, environment).
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

// emitOrchestratorNotification tags the orchestrator so the frontend can act on it.
func (s *Service) emitOrchestratorNotification(kind, orchestratorID, message, action string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	s.d.Emit(uimodel.AppNotificationEvent, uimodel.AppNotificationPayload{
		Kind:           kind,
		Message:        message,
		OrchestratorID: orchestratorID,
		Action:         action,
	})
}

// notificationSourceOrchestratorEdgeUnreachable is posted per linked env, carrying that env's deploy action.
const notificationSourceOrchestratorEdgeUnreachable = "orchestrator-edge-unreachable"

const notificationActionRestartOrchestrator = "restart-orchestrator"

// notificationActionInstallAndRestartOrchestrator also links the CLI install docs.
const notificationActionInstallAndRestartOrchestrator = "install-and-restart-orchestrator"

// orchestratorShellActivityPayload is emitted every tick so a missed event self-heals.
type orchestratorShellActivityPayload struct {
	SessionID     int    `json:"sessionId"`
	Running       bool   `json:"running"`
	Command       string `json:"command,omitempty"`
	StartedAtUnix int64  `json:"startedAtUnix,omitempty"`
}
