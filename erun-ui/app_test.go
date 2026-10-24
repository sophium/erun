package main

import (
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// Note: the previous TestLoadIdleStatusStopsLinkedCloudContextWhenStopEligible
// test verified that LoadIdleStatus would itself fire ec2:StopInstances
// when the env became eligible. That behavior moved out of the desktop —
// the shared `MaybeArmOrFireIdleStop` in erun-common
// is now the only arbiter and it is driven exclusively by the in-pod
// monitor via `erun activity stop-ready`. The desktop is a pure
// observer (renders the warning pill, exposes the Cancel button via
// MCP). The arm→wait→fire transitions are covered by integration
// scenarios under erun-integration/testdata/activity.

// stubStore is the app's store as the stub every test app is built on.
func stubStore(app *App) stubUIStore {
	store, ok := app.deps.store.(stubUIStore)
	if !ok {
		panic(fmt.Sprintf("app store is %T, not stubUIStore", app.deps.store))
	}
	return store
}

// tcpListenerPort is the port a test's TCP listener bound.
func tcpListenerPort(t *testing.T, listener net.Listener) int {
	t.Helper()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %v is not TCP", listener.Addr())
	}
	return addr.Port
}

type stubUIStore struct {
	config         *configpkg.ERunConfig
	projectConfigs map[string]configpkg.ProjectConfig
	tenants        map[string]configpkg.TenantConfig
	envs           map[string]configpkg.EnvConfig
	// listTenantsErr lets a test simulate a genuinely fresh install, where
	// ResolveListResult's ListTenantConfigs call itself fails with
	// ErrNotInitialized rather than succeeding with zero tenants.
	listTenantsErr error
}

func testCloudContextDeps(actions *[]string) cloud.CloudContextDependencies {
	return cloud.CloudContextDependencies{
		RunAWS: func(_ core.Context, _ configpkg.CloudProviderConfig, _ string, args []string) (string, error) {
			*actions = append(*actions, "aws "+strings.Join(args, " "))
			if strings.Join(args, " ") == "ec2 describe-instances --instance-ids i-test --query Reservations[0].Instances[0].PublicIpAddress --output text" {
				return "203.0.113.10", nil
			}
			return "", nil
		},
		RunKubectl: func(_ core.Context, args []string) error {
			*actions = append(*actions, "kubectl "+strings.Join(args, " "))
			return nil
		},
		// Pin "now" inside the default 08:00-20:00 working-hours window so
		// tests that exercise StartCloudContext are not sensitive to the
		// wall clock when the suite happens to run.
		Now: func() time.Time { return time.Date(2026, 5, 8, 12, 0, 0, 0, time.Local) },
	}
}

// stubUIStoreConfigMu guards the root config a stubUIStore points at. Opening
// an environment reads it from the runtime-ensure goroutine while the test's
// own goroutine may be saving it, and the stub is copied by value, so the lock
// cannot live on the stub itself.
var stubUIStoreConfigMu sync.Mutex

func (s stubUIStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	stubUIStoreConfigMu.Lock()
	defer stubUIStoreConfigMu.Unlock()
	if s.config == nil {
		return configpkg.ERunConfig{}, "", nil
	}
	return *s.config, "", nil
}

func (s stubUIStore) LoadProjectConfig(projectRoot string) (configpkg.ProjectConfig, string, error) {
	config, ok := s.projectConfigs[projectRoot]
	if !ok {
		return configpkg.ProjectConfig{}, "", configpkg.ErrNotInitialized
	}
	return config, "", nil
}

func (s stubUIStore) SaveProjectConfig(projectRoot string, config configpkg.ProjectConfig) error {
	if s.projectConfigs != nil {
		s.projectConfigs[projectRoot] = config
	}
	return nil
}

func (s stubUIStore) SaveERunConfig(config configpkg.ERunConfig) error {
	stubUIStoreConfigMu.Lock()
	defer stubUIStoreConfigMu.Unlock()
	if s.config != nil {
		*s.config = config
	}
	return nil
}

func (s stubUIStore) LoadTenantConfig(name string) (configpkg.TenantConfig, string, error) {
	config, ok := s.tenants[name]
	if !ok {
		return configpkg.TenantConfig{}, "", configpkg.ErrNotInitialized
	}
	return config, "", nil
}

func (s stubUIStore) SaveTenantConfig(config configpkg.TenantConfig) error {
	if s.tenants == nil {
		s.tenants = make(map[string]configpkg.TenantConfig)
	}
	s.tenants[config.Name] = config
	return nil
}

func (s stubUIStore) DeleteTenantConfig(tenant string) error {
	delete(s.tenants, tenant)
	return nil
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
		s.envs = make(map[string]configpkg.EnvConfig)
	}
	s.envs[tenant+"/"+config.Name] = config
	return nil
}

func (s stubUIStore) DeleteEnvConfig(tenant, environment string) error {
	delete(s.envs, tenant+"/"+environment)
	return nil
}

func (s stubUIStore) ListTenantConfigs() ([]configpkg.TenantConfig, error) {
	if s.listTenantsErr != nil {
		return nil, s.listTenantsErr
	}
	tenants := make([]configpkg.TenantConfig, 0, len(s.tenants))
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
	// pid is 0 unless a test needs the session to be trackable by process — the
	// investigation job records a pid and resolves its state from it.
	pid int
}

// stubSessionReadyOutput is the line every newStubTerminalSession emits
// on its first Read so the desktop's session-ready trace handler
// observes the equivalent of `kubectl exec` attaching, signals the
// session ready, and releases the per-env action runner gate. Without
// this, tests calling StartSession twice in a row would wedge on the
// second call's enqueue waiting 10 min for the first action's gate.
const stubSessionReadyOutput = `Defaulted container "stub" out of: stub` + "\n"

func newStubTerminalSession() *stubTerminalSession {
	return &stubTerminalSession{
		closeCh:       make(chan struct{}),
		initialOutput: []byte(stubSessionReadyOutput),
	}
}

// newSilentStubTerminalSession emits nothing, so streamSession never records
// output activity. Use it where a test counts activity events and the ready-line
// gate is not needed.
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
