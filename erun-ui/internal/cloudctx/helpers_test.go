package cloudctx

import (
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

type fakeStore struct {
	config  configpkg.ERunConfig
	tenants map[string]model.TenantConfig
	envs    map[string]configpkg.EnvConfig
}

func (s fakeStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return s.config, "", nil
}

func (s fakeStore) LoadTenantConfig(tenant string) (model.TenantConfig, string, error) {
	cfg, ok := s.tenants[tenant]
	if !ok {
		return model.TenantConfig{}, "", configpkg.ErrNotInitialized
	}
	return cfg, "", nil
}

func (s fakeStore) LoadEnvConfig(tenant, environment string) (configpkg.EnvConfig, string, error) {
	cfg, ok := s.envs[tenant+"/"+environment]
	if !ok {
		return configpkg.EnvConfig{}, "", configpkg.ErrNotInitialized
	}
	return cfg, "", nil
}

// The listings let ResolveOpen derive the environment's local port range.
func (s fakeStore) ListTenantConfigs() ([]model.TenantConfig, error) {
	tenants := make([]model.TenantConfig, 0, len(s.tenants))
	for _, tenant := range s.tenants {
		tenants = append(tenants, tenant)
	}
	return tenants, nil
}

func (s fakeStore) ListEnvConfigs(tenant string) ([]configpkg.EnvConfig, error) {
	var envs []configpkg.EnvConfig
	for key, env := range s.envs {
		if strings.HasPrefix(key, tenant+"/") {
			envs = append(envs, env)
		}
	}
	return envs, nil
}

type notification struct {
	selection uimodel.Selection
	message   string
}

type notifications struct {
	mu   sync.Mutex
	list []notification
}

func (n *notifications) record(selection uimodel.Selection, message string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.list = append(n.list, notification{selection: selection, message: message})
}

func (n *notifications) all() []notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]notification(nil), n.list...)
}

// newTestService wires every seam to an inert fake; no test reaches a real MCP endpoint.
func newTestService(t *testing.T, store Store, notify *notifications) *Service {
	t.Helper()
	if notify == nil {
		notify = &notifications{}
	}
	return New(Deps{
		Store:        store,
		MCPBearer:    func(string, string) string { return "" },
		MCPReachable: func(int) bool { return false },
		MCPEndpoint:  func(configpkg.OpenResult) string { return "http://127.0.0.1:0/mcp" },
		MCPTransport: func(string, string) mcp.Transport {
			t.Fatal("unexpected MCP connection")
			return nil
		},
		ClientVersion:        func() string { return "test" },
		NotifyRefreshFailure: notify.record,
		OnStatusChange:       func() {},
	})
}
