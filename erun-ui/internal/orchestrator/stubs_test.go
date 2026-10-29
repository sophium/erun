package orchestrator

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

func stubStore(app *testApp) stubUIStore {
	store, ok := app.deps.store.(stubUIStore)
	if !ok {
		panic(fmt.Sprintf("app store is %T, not stubUIStore", app.deps.store))
	}
	return store
}

// liveStore reads the test's store per call, so a test may swap it after building.
type liveStore struct {
	deps *testDeps
}

func (s liveStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return s.deps.store.LoadERunConfig()
}

func (s liveStore) LoadTenantConfig(name string) (model.TenantConfig, string, error) {
	return s.deps.store.LoadTenantConfig(name)
}

func (s liveStore) LoadEnvConfig(tenant, environment string) (configpkg.EnvConfig, string, error) {
	return s.deps.store.LoadEnvConfig(tenant, environment)
}

func (s liveStore) ListTenantConfigs() ([]model.TenantConfig, error) {
	return s.deps.store.ListTenantConfigs()
}

func (s liveStore) ListEnvConfigs(tenant string) ([]configpkg.EnvConfig, error) {
	return s.deps.store.ListEnvConfigs(tenant)
}

func (s liveStore) SaveERunConfig(config configpkg.ERunConfig) error {
	return s.deps.store.SaveERunConfig(config)
}

func (s liveStore) SaveEnvConfig(tenant string, config configpkg.EnvConfig) error {
	return s.deps.store.SaveEnvConfig(tenant, config)
}

type stubUIStore struct {
	config         *configpkg.ERunConfig
	projectConfigs map[string]configpkg.ProjectConfig
	tenants        map[string]model.TenantConfig
	envs           map[string]configpkg.EnvConfig
	listTenantsErr error
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
	pid           int
}

func newStubTerminalSession() *stubTerminalSession {
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

func (s *stubTerminalSession) Resize(int, int) error { return nil }

func (s *stubTerminalSession) Wait() error { return s.waitErr }

func (s *stubTerminalSession) Pid() int { return s.pid }

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

type capturedEmits struct {
	mu      sync.Mutex
	arrived *sync.Cond
	byName  map[string][]any
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

func (c *capturedEmits) events(name string) []any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]any, len(c.byName[name]))
	copy(out, c.byName[name])
	return out
}

func mustNoErr(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// restoreLogOutputAfter puts the standard logger back once the test has captured it.
func restoreLogOutputAfter(t *testing.T) {
	t.Helper()
	previous := log.Writer()
	t.Cleanup(func() { log.SetOutput(previous) })
}

// workingIssueCommandRunner is the shape of the gh boundary claim labels go through.
type workingIssueCommandRunner = func(ctx context.Context, dir, name string, args ...string) (string, error)
