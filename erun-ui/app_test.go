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
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-ui/internal/terminal"
)

func stubStore(app *App) stubUIStore {
	store, ok := app.deps.store.(stubUIStore)
	if !ok {
		panic(fmt.Sprintf("app store is %T, not stubUIStore", app.deps.store))
	}
	return store
}

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
	tenants        map[string]model.TenantConfig
	envs           map[string]configpkg.EnvConfig
	// listTenantsErr simulates a fresh install where ListTenantConfigs fails with ErrNotInitialized.
	listTenantsErr error
}

func testCloudContextDeps(actions *[]string) cloud.CloudContextDependencies {
	return cloud.CloudContextDependencies{
		RunAWS: func(_ core.Context, _ model.CloudProviderConfig, _ string, args []string) (string, error) {
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
		// Pin "now" inside the 08:00-20:00 working-hours window.
		Now: func() time.Time { return time.Date(2026, 5, 8, 12, 0, 0, 0, time.Local) },
	}
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

func (s stubUIStore) LoadTenantConfig(name string) (model.TenantConfig, string, error) {
	config, ok := s.tenants[name]
	if !ok {
		return model.TenantConfig{}, "", configpkg.ErrNotInitialized
	}
	return config, "", nil
}

func (s stubUIStore) SaveTenantConfig(config model.TenantConfig) error {
	if s.tenants == nil {
		s.tenants = make(map[string]model.TenantConfig)
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

func (s stubUIStore) ListTenantConfigs() ([]model.TenantConfig, error) {
	if s.listTenantsErr != nil {
		return nil, s.listTenantsErr
	}
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

// withTestEnvops gives a hand-built App the envops service NewApp would build,
// for tests that construct App directly and reach an environment operation.
// withTestTerminal gives a hand-built App the terminal service NewApp would build.
func withTestTerminal(app *App) *App {
	store := app.deps.store
	if store == nil {
		app.deps.store = stubUIStore{}
	}
	app.terminal = app.newTerminalService()
	app.deps.store = store
	return app
}

// setTestSession registers managed under key, as a test that seeds the registry directly needs.
func setTestSession(app *App, key string, managed *terminal.Session) {
	app.terminal.WithLock(func(l *terminal.Locked) {
		l.Sessions()[key] = managed
	})
}

type capturedEmits struct {
	mu       sync.Mutex
	arrived  *sync.Cond
	byName   map[string][]any
	deadline bool
}

func newCapturedEmits() *capturedEmits {
	c := &capturedEmits{byName: make(map[string][]any)}
	c.arrived = sync.NewCond(&c.mu)
	return c
}

func (c *capturedEmits) fn() func(string, ...any) {
	return func(name string, args ...any) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.byName[name] = append(c.byName[name], args...)
		if len(args) == 0 {
			c.byName[name] = append(c.byName[name], nil)
		}
		c.arrived.Broadcast()
	}
}

// waitFor waits on the emitter's signal rather than a wall-clock poll, so CPU
// contention cannot fail it; pred runs under the lock.
func (c *capturedEmits) waitFor(bound time.Duration, pred func(byName map[string][]any) bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadline = false
	timer := time.AfterFunc(bound, func() {
		c.mu.Lock()
		c.deadline = true
		c.mu.Unlock()
		c.arrived.Broadcast()
	})
	defer timer.Stop()
	for !pred(c.byName) {
		if c.deadline {
			return false
		}
		c.arrived.Wait()
	}
	return true
}

func (c *capturedEmits) events(name string) []any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]any, len(c.byName[name]))
	copy(out, c.byName[name])
	return out
}

func waitForSessionCount(t *testing.T, mu *sync.Mutex, sessions *[]*stubTerminalSession, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := len(*sessions)
		mu.Unlock()
		if got >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	got := len(*sessions)
	mu.Unlock()
	t.Fatalf("timed out waiting for %d sessions, got %d", want, got)
}

// lockTerminalForTest holds the terminal lock until unlock runs, so a test can
// seed or read state that shares it across several statements.
func lockTerminalForTest(app *App) (*terminal.Locked, func()) {
	held := make(chan *terminal.Locked)
	release := make(chan struct{})
	go app.terminal.WithLock(func(l *terminal.Locked) {
		held <- l
		<-release
	})
	return <-held, func() { close(release) }
}
