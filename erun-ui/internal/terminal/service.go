// Package terminal owns the desktop's PTY sessions: starting, reusing, streaming,
// input, resizing, reconnecting and closing them, the AI busy latch and the pod
// heartbeat that releases it, idle status, and the commands piped into an
// environment's Local shell.
package terminal

import (
	"context"
	"strings"
	"sync"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/envstore"
	"github.com/sophium/erun/erun-common/base/seam/kubeapi"
	"github.com/sophium/erun/erun-common/base/seam/projectroot"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Store is the slice of the desktop's config store the sessions read. A delete
// also needs seam.DeleteStore, which DeleteEnvironment asserts at call time.
type Store interface {
	envstore.ListStore
}

// GatedStart starts a session once the per-env action runner admits it; the
// session it returns gates the runner until it is ready.
type GatedStart func(ctx context.Context) (uimodel.StartSessionResult, *Session, error)

type Deps struct {
	Store           Store
	FindProjectRoot projectroot.ProjectFinderFunc
	// Context is read per call: the Wails context arrives after the service is built.
	Context func() context.Context
	// Started reports whether startup has run.
	Started func() bool
	Emit    uimodel.Emitter
	// AppSessionEnv marks every process the desktop launches as its own ("NAME=1").
	AppSessionEnv string

	ResolveCLIPath            func() string
	StartTerminal             func(StartParams) (PTY, error)
	RunIDECommand             func(context.Context, StartParams) (string, error)
	RecordEnvironmentActivity func(model.EnvironmentActivityParams) error
	SavePastedFile            func(PasteParams) (string, error)
	LoadDiff                  func(ctx context.Context, endpoint, bearer string, options uimodel.DiffOptions) (model.DiffResult, error)
	LoadIdleStatus            func(ctx context.Context, endpoint, bearer string) (model.EnvironmentIdleStatus, error)
	EnsureMCP                 func(context.Context, config.OpenResult) error
	ReconnectMCP              func(ctx context.Context, result config.OpenResult, onLine func(string)) error
	DeleteNamespace           kubeapi.NamespaceDeleterFunc
	// ExecRuntimePod uses kubectl exec rather than the MCP edge, so it works while the edge is down.
	ExecRuntimePod   func(ctx context.Context, selection uimodel.Selection, script string) (string, error)
	ProbePodSessions func(uimodel.Selection) ([]PodSession, error)

	CanConnectLocalPort func(port int) bool
	// CanReachMCPEndpoint checks traffic, not a dial: a stale port-forward accepts and never answers.
	CanReachMCPEndpoint func(port int) bool
	MCPEndpoint         func(config.OpenResult) string
	MCPBearer           func(tenant, environment string) string
	WrapMCPUnreachable  func(kind mcp.LocalMCPUnreachableKind, err error) error
	IsMCPDialFailure    func(err error) bool
	MCPTransport        func(endpoint, bearer string) mcpsdk.Transport
	ClientVersion       func() string

	LinkedCloudContext              func(config.EnvConfig) (model.CloudContextStatus, bool, error)
	EnsureLinkedCloudContextRunning func(config.EnvConfig) (model.CloudContextStatus, bool, error)
	StopCloudContext                func(name string) (model.CloudContextStatus, error)
	StartCloudRefresher             func(uimodel.Selection)
	StopCloudRefresher              func(uimodel.Selection)

	EnsureRuntimeOnce   func(uimodel.Selection)
	RuntimeStopped      func(uimodel.Selection) bool
	ClearRuntimeStopped func(uimodel.Selection)
	StartWorkspaceSync  func(uimodel.Selection)
	StopWorkspaceSync   func(uimodel.Selection)
	ClearInitEmitted    func(tenant, environment string)
	// EnvironmentJobIDs lists the ids of the env's job records.
	EnvironmentJobIDs func(tenant, environment string) ([]string, error)

	// EnqueueGated runs start through the per-env action runner, so a parallel
	// open cannot duplicate a build and deploy.
	EnqueueGated func(selection uimodel.Selection, kind string, start GatedStart) (uimodel.StartSessionResult, error)
	// OnOutput feeds each chunk to the activity trace; it is called without the terminal lock held.
	OnOutput                      func(*Session, []byte)
	RememberKubeContext           func(name string)
	LockIfDeployInFlight          func(selection uimodel.Selection, serial int)
	LatestDeployFailed            func(tenant, environment string) bool
	AppendMCPAuthPublicKeyFlag    func(args []string) []string
	RunDeployOrchestration        func(ctx context.Context, selection uimodel.Selection, result config.OpenResult, force bool) error
	MaybeStartDeployOrchestration func(selection uimodel.Selection, force bool) (uimodel.StartSessionResult, bool)

	RearmOrchestratorPacing func(id string)
	// OrchestratorRegisteredLocked is called with the terminal lock held: the
	// orchestrator registry shares that lock until it moves out of main.
	OrchestratorRegisteredLocked func(id string) bool
}

type Service struct {
	d Deps

	// mu is the desktop's outermost lock. It guards the session registry and the
	// per-session state below, and callers outside this package take it through
	// WithLock.
	mu         sync.Mutex
	nextSerial int
	sessions   map[string]*Session
	// sessionWG lets shutdown wait for every stream reader goroutine to exit.
	sessionWG        sync.WaitGroup
	idleStops        map[string]struct{}
	intentionalStops map[string]struct{}
	// sessionHeartbeats is the latest pod observation of live sessions per environment.
	sessionHeartbeats map[string]sessionHeartbeat
	aiSessionReadings map[string]aiSessionReading
	busyEnvs          map[string]int
}

// New panics on a missing seam rather than defaulting it, so a test can never
// reach a real process or network call it did not wire.
func New(d Deps) *Service {
	if missing := missingSeams(d); len(missing) > 0 {
		panic("terminal.New: missing " + strings.Join(missing, ", "))
	}
	return &Service{
		d:                 d,
		sessions:          make(map[string]*Session),
		idleStops:         make(map[string]struct{}),
		intentionalStops:  make(map[string]struct{}),
		sessionHeartbeats: make(map[string]sessionHeartbeat),
		busyEnvs:          make(map[string]int),
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
		{"AppSessionEnv", d.AppSessionEnv == ""},
		{"ResolveCLIPath", d.ResolveCLIPath == nil},
		{"StartTerminal", d.StartTerminal == nil},
		{"RunIDECommand", d.RunIDECommand == nil},
		{"RecordEnvironmentActivity", d.RecordEnvironmentActivity == nil},
		{"SavePastedFile", d.SavePastedFile == nil},
		{"LoadDiff", d.LoadDiff == nil},
		{"LoadIdleStatus", d.LoadIdleStatus == nil},
		{"EnsureMCP", d.EnsureMCP == nil},
		{"ReconnectMCP", d.ReconnectMCP == nil},
		{"DeleteNamespace", d.DeleteNamespace == nil},
		{"ExecRuntimePod", d.ExecRuntimePod == nil},
		{"ProbePodSessions", d.ProbePodSessions == nil},
		{"CanConnectLocalPort", d.CanConnectLocalPort == nil},
		{"CanReachMCPEndpoint", d.CanReachMCPEndpoint == nil},
		{"MCPEndpoint", d.MCPEndpoint == nil},
		{"MCPBearer", d.MCPBearer == nil},
		{"WrapMCPUnreachable", d.WrapMCPUnreachable == nil},
		{"IsMCPDialFailure", d.IsMCPDialFailure == nil},
		{"MCPTransport", d.MCPTransport == nil},
		{"ClientVersion", d.ClientVersion == nil},
		{"LinkedCloudContext", d.LinkedCloudContext == nil},
		{"EnsureLinkedCloudContextRunning", d.EnsureLinkedCloudContextRunning == nil},
		{"StopCloudContext", d.StopCloudContext == nil},
		{"StartCloudRefresher", d.StartCloudRefresher == nil},
		{"StopCloudRefresher", d.StopCloudRefresher == nil},
		{"EnsureRuntimeOnce", d.EnsureRuntimeOnce == nil},
		{"RuntimeStopped", d.RuntimeStopped == nil},
		{"ClearRuntimeStopped", d.ClearRuntimeStopped == nil},
		{"StartWorkspaceSync", d.StartWorkspaceSync == nil},
		{"StopWorkspaceSync", d.StopWorkspaceSync == nil},
		{"ClearInitEmitted", d.ClearInitEmitted == nil},
		{"EnvironmentJobIDs", d.EnvironmentJobIDs == nil},
		{"EnqueueGated", d.EnqueueGated == nil},
		{"OnOutput", d.OnOutput == nil},
		{"RememberKubeContext", d.RememberKubeContext == nil},
		{"LockIfDeployInFlight", d.LockIfDeployInFlight == nil},
		{"LatestDeployFailed", d.LatestDeployFailed == nil},
		{"AppendMCPAuthPublicKeyFlag", d.AppendMCPAuthPublicKeyFlag == nil},
		{"RunDeployOrchestration", d.RunDeployOrchestration == nil},
		{"MaybeStartDeployOrchestration", d.MaybeStartDeployOrchestration == nil},
		{"RearmOrchestratorPacing", d.RearmOrchestratorPacing == nil},
		{"OrchestratorRegisteredLocked", d.OrchestratorRegisteredLocked == nil},
	}
	var missing []string
	for _, seam := range required {
		if seam.missing {
			missing = append(missing, seam.name)
		}
	}
	return missing
}

// Locked is the session registry as seen from inside WithLock. It must not
// escape the callback.
type Locked struct {
	s *Service
}

// WithLock runs fn under the terminal lock, so a caller can read or change
// sessions together with state of its own that shares this lock.
func (s *Service) WithLock(fn func(*Locked)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&Locked{s: s})
}

func (l *Locked) Session(key string) *Session { return l.s.sessions[key] }

// Sessions is the live registry; range over it, change it only through Register and Delete.
func (l *Locked) Sessions() map[string]*Session { return l.s.sessions }

func (l *Locked) Register(managed *Session) { l.s.sessions[managed.Key] = managed }

func (l *Locked) Delete(key string) { delete(l.s.sessions, key) }

// NextSerial allocates the next session id.
func (l *Locked) NextSerial() int {
	l.s.nextSerial++
	return l.s.nextSerial
}

// AddBusy counts a session that blocks the env's idle stop until its first output.
func (l *Locked) AddBusy(selection uimodel.Selection) {
	l.s.busyEnvs[uimodel.SelectionKey(selection)]++
}

// MarkClosed marks managed closed by a teardown and returns the PTY to close
// once the lock is released.
func (l *Locked) MarkClosed(managed *Session) PTY { return l.s.closeManagedLocked(managed) }

func (l *Locked) TypedRecently(managed *Session) bool { return typedRecentlyLocked(managed) }

// SourceMCPUnreachable tags the warning a dead MCP edge posts.
const SourceMCPUnreachable = "mcp-unreachable"

func (s *Service) emitEvent(name string, payload any) {
	s.d.Emit(name, payload)
}

// emitAppNotification posts a toast with no environment identity.
func (s *Service) emitAppNotification(kind, message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	s.d.Emit(uimodel.AppNotificationEvent, uimodel.AppNotificationPayload{Kind: kind, Message: message})
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
