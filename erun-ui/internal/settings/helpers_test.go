package settings

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle/gatewayrouting"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// newTestService fills every seam d leaves unset with an inert fake, so no test
// reaches a process, a cluster or the platform it did not wire.
func newTestService(d Deps) *Service {
	set := reflect.ValueOf(&d).Elem()
	fakes := reflect.ValueOf(inertDeps())
	for i := range set.NumField() {
		if set.Field(i).IsZero() {
			set.Field(i).Set(fakes.Field(i))
		}
	}
	return New(d)
}

func inertDeps() Deps {
	return Deps{
		Store:         fakeStore{},
		Context:       context.Background,
		Emit:          func(string, ...any) {},
		CloudStatuses: newFakeCloudStatuses(),
		StopCloudContext: func(context.Context, string) (model.CloudContextStatus, error) {
			return model.CloudContextStatus{}, errors.New("stop cloud context is not wired in this test")
		},
		MarkIntentionalStop:  func(string) {},
		ClearIntentionalStop: func(string) {},
		ClearIdleStops:       func(string) {},
		RecordManualStop:     func(context.Context, string) {},
		EnsureMCPAvailable:   func(context.Context, configpkg.OpenResult) error { return nil },
		MCPEndpoint: func(result configpkg.OpenResult) string {
			return fmt.Sprintf("http://127.0.0.1:%d/mcp", configpkg.MCPPortForResult(result))
		},
		MCPBearer: func(string, string) string { return "" },
		SetRemoteCloudAlias: func(context.Context, string, string, string, string, string) (configpkg.EnvConfig, error) {
			return configpkg.EnvConfig{}, errors.New("remote cloud alias is not wired in this test")
		},
		ReconcileWorkspaceSync: func(uimodel.Selection, bool) {},
		WorkspaceSyncStatus:    func(uimodel.Selection) (string, string) { return "stopped", "" },
		PlatformReady:          func(string) bool { return false },
		PlatformClient: func(context.Context, string) (*platformclient.PlatformClient, context.Context, context.CancelFunc, error) {
			return nil, nil, nil, errors.New("the platform is not wired in this test")
		},
		PlatformRequestTimeout: 10 * time.Second,
		ReadGatewayRouting: func(context.Context, configpkg.EnvConfig, string, *configpkg.OpenRouterConfig) (gatewayrouting.GatewayRouting, bool, error) {
			return gatewayrouting.GatewayRouting{}, false, nil
		},
	}
}

type fakeStore struct {
	config         *configpkg.ERunConfig
	projectConfigs map[string]configpkg.ProjectConfig
	tenants        map[string]model.TenantConfig
	envs           map[string]configpkg.EnvConfig
}

// fakeStoreConfigMu guards the root config across goroutines; the fake is copied
// by value, so the lock cannot live on it.
var fakeStoreConfigMu sync.Mutex

func (s fakeStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	fakeStoreConfigMu.Lock()
	defer fakeStoreConfigMu.Unlock()
	if s.config == nil {
		return configpkg.ERunConfig{}, "", nil
	}
	return *s.config, "", nil
}

func (s fakeStore) SaveERunConfig(config configpkg.ERunConfig) error {
	fakeStoreConfigMu.Lock()
	defer fakeStoreConfigMu.Unlock()
	if s.config != nil {
		*s.config = config
	}
	return nil
}

func (s fakeStore) LoadProjectConfig(projectRoot string) (configpkg.ProjectConfig, string, error) {
	config, ok := s.projectConfigs[projectRoot]
	if !ok {
		return configpkg.ProjectConfig{}, "", configpkg.ErrNotInitialized
	}
	return config, "", nil
}

func (s fakeStore) SaveProjectConfig(projectRoot string, config configpkg.ProjectConfig) error {
	if s.projectConfigs != nil {
		s.projectConfigs[projectRoot] = config
	}
	return nil
}

func (s fakeStore) LoadTenantConfig(name string) (model.TenantConfig, string, error) {
	config, ok := s.tenants[name]
	if !ok {
		return model.TenantConfig{}, "", configpkg.ErrNotInitialized
	}
	return config, "", nil
}

func (s fakeStore) SaveTenantConfig(config model.TenantConfig) error {
	if s.tenants == nil {
		s.tenants = make(map[string]model.TenantConfig)
	}
	s.tenants[config.Name] = config
	return nil
}

func (s fakeStore) LoadEnvConfig(tenant, environment string) (configpkg.EnvConfig, string, error) {
	config, ok := s.envs[tenant+"/"+environment]
	if !ok {
		return configpkg.EnvConfig{}, "", configpkg.ErrNotInitialized
	}
	return config, "", nil
}

func (s fakeStore) SaveEnvConfig(tenant string, config configpkg.EnvConfig) error {
	if s.envs == nil {
		s.envs = make(map[string]configpkg.EnvConfig)
	}
	s.envs[tenant+"/"+config.Name] = config
	return nil
}

func (s fakeStore) ListTenantConfigs() ([]model.TenantConfig, error) {
	tenants := make([]model.TenantConfig, 0, len(s.tenants))
	for _, tenant := range s.tenants {
		tenants = append(tenants, tenant)
	}
	return tenants, nil
}

func (s fakeStore) ListEnvConfigs(tenant string) ([]configpkg.EnvConfig, error) {
	envs := make([]configpkg.EnvConfig, 0)
	for key, env := range s.envs {
		if strings.HasPrefix(key, tenant+"/") {
			envs = append(envs, env)
		}
	}
	return envs, nil
}

type fakeCloudStatuses struct {
	mu       sync.Mutex
	statuses map[string]string
}

func newFakeCloudStatuses() *fakeCloudStatuses {
	return &fakeCloudStatuses{statuses: make(map[string]string)}
}

func (c *fakeCloudStatuses) ApplyStatuses(statuses []model.CloudContextStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, status := range statuses {
		c.statuses[status.Name] = status.Status
	}
}

func (c *fakeCloudStatuses) SetStatus(name, status string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.statuses[name] = status
}

func (c *fakeCloudStatuses) Status(name string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.statuses[name]
}

func (c *fakeCloudStatuses) ReconcileRefresher(uimodel.Selection, bool) {}

type capturedEmits struct {
	mu     sync.Mutex
	byName map[string][]any
}

func newCapturedEmits() *capturedEmits {
	return &capturedEmits{byName: make(map[string][]any)}
}

func (c *capturedEmits) fn() uimodel.Emitter {
	return func(name string, args ...any) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.byName[name] = append(c.byName[name], args...)
	}
}

func (c *capturedEmits) events(name string) []any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]any(nil), c.byName[name]...)
}

func registryWithRole(entries []ContainerRegistryEntry, role string) string {
	for _, entry := range entries {
		for _, candidate := range entry.Roles {
			if candidate == role {
				return entry.Registry
			}
		}
	}
	return ""
}

func tcpListenerPort(t *testing.T, listener net.Listener) int {
	t.Helper()
	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address %v is not TCP", listener.Addr())
	}
	return addr.Port
}

func testUIJWT(issuer string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"` + issuer + `","sub":"test-subject"}`))
	return header + "." + payload + ".signature"
}
