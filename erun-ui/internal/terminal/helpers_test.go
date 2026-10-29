package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	configpkg "github.com/sophium/erun/erun-common/base/config"
	configcloudcontext "github.com/sophium/erun/erun-common/base/config/cloudcontext"
	"github.com/sophium/erun/erun-common/base/config/localports"
	"github.com/sophium/erun/erun-common/base/model"
	cloudcloudcontext "github.com/sophium/erun/erun-common/infra/cloud/cloudcontext"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// newTestService fills every seam d leaves unset with an inert fake, so no test
// reaches a process, a cluster or a pod it did not wire.
func newTestService(d Deps) *Service {
	set := reflect.ValueOf(&d).Elem()
	fakes := reflect.ValueOf(inertDeps(d))
	for i := range set.NumField() {
		if set.Field(i).IsZero() {
			set.Field(i).Set(fakes.Field(i))
		}
	}
	return New(d)
}

// closeTestService closes every session and waits for their readers, as the
// desktop's shutdown does.
func closeTestService(s *Service) {
	s.CloseAll()
	s.Wait()
}

// errMCPUnreachable stands in for the shell's marker, so a test can tell a
// wrapped unreachable error from a passed-through one.
var errMCPUnreachable = errors.New("mcp unreachable")

type unreachableError struct {
	kind mcp.LocalMCPUnreachableKind
	err  error
}

func (e unreachableError) Error() string {
	return fmt.Sprintf("unreachable (%v): %v", e.kind, e.err)
}

func (e unreachableError) Unwrap() []error { return []error{errMCPUnreachable, e.err} }

// inertDeps derives the cloud-context link from the test's own store, the way
// the settings service resolves it, with every context's power state unknown.
func inertDeps(d Deps) Deps {
	store := d.Store
	if store == nil {
		store = stubUIStore{}
	}
	return Deps{
		Store:           store,
		FindProjectRoot: func() (string, string, error) { return "", "", errors.New("no project in this test") },
		Context:         context.Background,
		Started:         func() bool { return false },
		Emit:            func(string, ...any) {},
		AppSessionEnv:   "ERUN_APP_SESSION=1",
		ResolveCLIPath:  func() string { return "erun" },
		StartTerminal: func(StartParams) (PTY, error) {
			return nil, errors.New("terminals are not wired in this test")
		},
		RunIDECommand: func(context.Context, StartParams) (string, error) {
			return "", errors.New("IDE launch is not wired in this test")
		},
		RecordEnvironmentActivity: func(model.EnvironmentActivityParams) error { return nil },
		SavePastedFile: func(PasteParams) (string, error) {
			return "", errors.New("pasting is not wired in this test")
		},
		LoadDiff: func(context.Context, string, string, uimodel.DiffOptions) (model.DiffResult, error) {
			return model.DiffResult{}, errors.New("diff is not wired in this test")
		},
		LoadIdleStatus: func(context.Context, string, string) (model.EnvironmentIdleStatus, error) {
			return model.EnvironmentIdleStatus{}, errors.New("idle status is not wired in this test")
		},
		EnsureMCP: func(context.Context, configpkg.OpenResult) error {
			return errors.New("MCP ensure is not wired in this test")
		},
		ReconnectMCP: func(context.Context, configpkg.OpenResult, func(string)) error {
			return errors.New("reconnect is not wired in this test")
		},
		DeleteNamespace: func(string, string) error { return errors.New("namespace delete is not wired in this test") },
		ExecRuntimePod: func(context.Context, uimodel.Selection, string) (string, error) {
			return "", errors.New("runtime pod exec is unavailable")
		},
		ProbePodSessions: func(uimodel.Selection) ([]PodSession, error) {
			return nil, errors.New("pod probe is not wired in this test")
		},
		CanConnectLocalPort: func(int) bool { return false },
		CanReachMCPEndpoint: func(int) bool { return true },
		MCPEndpoint: func(result configpkg.OpenResult) string {
			return fmt.Sprintf("http://127.0.0.1:%d/mcp", localports.MCPPortForResult(result))
		},
		MCPBearer: func(string, string) string { return "" },
		WrapMCPUnreachable: func(kind mcp.LocalMCPUnreachableKind, err error) error {
			return unreachableError{kind: kind, err: err}
		},
		IsMCPDialFailure: func(err error) bool {
			return err != nil && strings.Contains(err.Error(), "connection refused")
		},
		MCPTransport: func(endpoint, _ string) mcpsdk.Transport {
			return &mcpsdk.StreamableClientTransport{Endpoint: endpoint}
		},
		ClientVersion: func() string { return "test" },
		LinkedCloudContext: func(env configpkg.EnvConfig) (model.CloudContextStatus, bool, error) {
			return linkedCloudContextFromStore(store, env)
		},
		EnsureLinkedCloudContextRunning: func(env configpkg.EnvConfig) (model.CloudContextStatus, bool, error) {
			return linkedCloudContextFromStore(store, env)
		},
		StopCloudContext: func(name string) (model.CloudContextStatus, error) {
			return model.CloudContextStatus{}, errors.New("cloud context stop is not wired in this test")
		},
		StartCloudRefresher: func(uimodel.Selection) {},
		StopCloudRefresher:  func(uimodel.Selection) {},
		EnsureRuntimeOnce:   func(uimodel.Selection) {},
		RuntimeStopped:      func(uimodel.Selection) bool { return false },
		ClearRuntimeStopped: func(uimodel.Selection) {},
		StartWorkspaceSync:  func(uimodel.Selection) {},
		StopWorkspaceSync:   func(uimodel.Selection) {},
		ClearInitEmitted:    func(string, string) {},
		EnvironmentJobIDs: func(string, string) ([]string, error) {
			return nil, errors.New("pod jobs are not wired in this test")
		},
		EnqueueGated: func(_ uimodel.Selection, _ string, start GatedStart) (uimodel.StartSessionResult, error) {
			result, _, err := start(context.Background())
			return result, err
		},
		OnOutput:                   func(*Session, []byte) {},
		RememberKubeContext:        func(string) {},
		LockIfDeployInFlight:       func(uimodel.Selection, int) {},
		LatestDeployFailed:         func(string, string) bool { return false },
		AppendMCPAuthPublicKeyFlag: func(args []string) []string { return args },
		RunDeployOrchestration: func(context.Context, uimodel.Selection, configpkg.OpenResult, bool) error {
			return errors.New("deploy orchestration is not wired in this test")
		},
		MaybeStartDeployOrchestration: func(uimodel.Selection, bool) (uimodel.StartSessionResult, bool) {
			return uimodel.StartSessionResult{}, false
		},
		RearmOrchestratorPacing:      func(string) {},
		OrchestratorRegisteredLocked: func(string) bool { return false },
	}
}

// testCloudStatuses is the live power state the cloud service would report, per
// context name; tests in this package do not run in parallel.
var (
	testCloudStatusesMu sync.Mutex
	testCloudStatuses   = map[string]string{}
)

// setCloudContextStatus reports name's power state for the rest of the test.
func setCloudContextStatus(t *testing.T, name, status string) {
	t.Helper()
	testCloudStatusesMu.Lock()
	testCloudStatuses[name] = status
	testCloudStatusesMu.Unlock()
	t.Cleanup(func() {
		testCloudStatusesMu.Lock()
		delete(testCloudStatuses, name)
		testCloudStatusesMu.Unlock()
	})
}

// linkedCloudContextFromStore links an env to the configured cloud context
// whose Kubernetes context or name it uses, as the settings service does.
func linkedCloudContextFromStore(store Store, env configpkg.EnvConfig) (model.CloudContextStatus, bool, error) {
	kubernetesContext := strings.TrimSpace(env.KubernetesContext)
	if kubernetesContext == "" {
		return model.CloudContextStatus{}, false, nil
	}
	statuses, err := cloudcloudcontext.ListCloudContextStatuses(store)
	if err != nil {
		return model.CloudContextStatus{}, false, err
	}
	for _, status := range statuses {
		context := configcloudcontext.NormalizeCloudContextConfig(status.CloudContextConfig)
		if strings.TrimSpace(context.KubernetesContext) == kubernetesContext || strings.TrimSpace(context.Name) == kubernetesContext {
			status.CloudContextConfig = context
			testCloudStatusesMu.Lock()
			status.Status = testCloudStatuses[context.Name]
			testCloudStatusesMu.Unlock()
			return status, true, nil
		}
	}
	return model.CloudContextStatus{}, false, nil
}

type stubUIStore struct {
	config  *configpkg.ERunConfig
	tenants map[string]model.TenantConfig
	envs    map[string]configpkg.EnvConfig
}

// stubUIStoreConfigMu guards the root config across goroutines; the stub is copied
// by value, so the lock cannot live on it.
var stubUIStoreConfigMu sync.Mutex

func (s stubUIStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	stubUIStoreConfigMu.Lock()
	defer stubUIStoreConfigMu.Unlock()
	if s.config == nil {
		return configpkg.ERunConfig{}, "", nil
	}
	return *s.config, "", nil
}

func (s stubUIStore) LoadProjectConfig(string) (configpkg.ProjectConfig, string, error) {
	return configpkg.ProjectConfig{}, "", configpkg.ErrNotInitialized
}

func (s stubUIStore) LoadTenantConfig(name string) (model.TenantConfig, string, error) {
	config, ok := s.tenants[name]
	if !ok {
		return model.TenantConfig{}, "", configpkg.ErrNotInitialized
	}
	return config, "", nil
}

func (s stubUIStore) LoadEnvConfig(tenant, environment string) (configpkg.EnvConfig, string, error) {
	config, ok := s.envs[tenant+"/"+environment]
	if !ok {
		return configpkg.EnvConfig{}, "", configpkg.ErrNotInitialized
	}
	return config, "", nil
}

func (s stubUIStore) SaveEnvConfig(tenant string, config configpkg.EnvConfig) error {
	if s.envs == nil {
		return errors.New("stub store has no environments to save into")
	}
	s.envs[tenant+"/"+config.Name] = config
	return nil
}

func (s stubUIStore) DeleteEnvConfig(tenant, environment string) error {
	delete(s.envs, tenant+"/"+environment)
	return nil
}

func (s stubUIStore) DeleteTenantConfig(tenant string) error {
	delete(s.tenants, tenant)
	return nil
}

func (s stubUIStore) ListTenantConfigs() ([]model.TenantConfig, error) {
	tenants := make([]model.TenantConfig, 0, len(s.tenants))
	for _, tenant := range s.tenants {
		tenants = append(tenants, tenant)
	}
	return tenants, nil
}

func (s stubUIStore) ListEnvConfigs(tenant string) ([]configpkg.EnvConfig, error) {
	envs := make([]configpkg.EnvConfig, 0)
	for key, env := range s.envs {
		if strings.HasPrefix(key, tenant+"/") {
			envs = append(envs, env)
		}
	}
	return envs, nil
}

type stubTerminalSession struct {
	closeCh       chan struct{}
	waitErr       error
	mu            sync.Mutex
	written       []byte
	initialOutput []byte
	resizes       [][2]int
	pid           int
}

// stubSessionReadyOutput mimics `kubectl exec` attaching so the session signals
// ready and releases the per-env action gate.
const stubSessionReadyOutput = `Defaulted container "stub" out of: stub` + "\n"

func newStubTerminalSession() *stubTerminalSession {
	return &stubTerminalSession{
		closeCh:       make(chan struct{}),
		initialOutput: []byte(stubSessionReadyOutput),
	}
}

// newSilentStubTerminalSession emits nothing, so no output activity is recorded.
func newSilentStubTerminalSession() *stubTerminalSession {
	return &stubTerminalSession{closeCh: make(chan struct{})}
}

func (s *stubTerminalSession) Read(buf []byte) (int, error) {
	s.mu.Lock()
	if len(s.initialOutput) > 0 {
		n := copy(buf, s.initialOutput)
		s.initialOutput = s.initialOutput[n:]
		s.mu.Unlock()
		return n, nil
	}
	s.mu.Unlock()
	<-s.closeCh
	return 0, io.EOF
}

func (s *stubTerminalSession) Write(buffer []byte) (int, error) {
	s.mu.Lock()
	s.written = append(s.written, buffer...)
	s.mu.Unlock()
	return len(buffer), nil
}

func (s *stubTerminalSession) WrittenString() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.written)
}

func (s *stubTerminalSession) Resize(cols, rows int) error {
	s.mu.Lock()
	s.resizes = append(s.resizes, [2]int{cols, rows})
	s.mu.Unlock()
	return nil
}

func (s *stubTerminalSession) Resizes() [][2]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][2]int(nil), s.resizes...)
}

func (s *stubTerminalSession) Wait() error {
	return s.waitErr
}

func (s *stubTerminalSession) Pid() int {
	return s.pid
}

func (s *stubTerminalSession) Alive() bool {
	select {
	case <-s.closeCh:
		return false
	default:
		return true
	}
}

func (s *stubTerminalSession) Close() error {
	select {
	case <-s.closeCh:
	default:
		close(s.closeCh)
	}
	return nil
}
