package runtimeview

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/localports"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// newTestService fills every seam d leaves unset with an inert fake, so no test
// reaches a process, a cluster or a registry it did not wire.
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
		Store:   fakeStore{},
		Context: context.Background,
		Emit:    func(string, ...any) {},
		ExecRuntimePod: func(context.Context, uimodel.Selection, string) (string, error) {
			return "", errors.New("runtime pod exec is not wired in this test")
		},
		LoadUsage: func(context.Context, uimodel.Selection) (Usage, error) {
			return Usage{}, errors.New("runtime usage is not wired in this test")
		},
		LoadResourceStatus: func(context.Context, ResourceInput) (ResourceStatus, error) {
			return ResourceStatus{}, errors.New("node capacity is not wired in this test")
		},
		LoadClusterRegistry: func(context.Context, ResourceInput) (ClusterRegistryStatus, error) {
			return ClusterRegistryStatus{}, errors.New("cluster registry is not wired in this test")
		},
		LoadHostedRegistry: func(context.Context) HostedRegistryStatus { return HostedRegistryStatus{} },
		CheckRuntimeDeployed: func(context.Context, string, string, string) (bool, error) {
			return false, errors.New("runtime deployment check is not wired in this test")
		},
		ResolveImageRegistry: func(context.Context, string, string) (model.RuntimeRegistryVersions, error) {
			return model.RuntimeRegistryVersions{}, errors.New("image registry is not wired in this test")
		},
		ChartAvailabilityOverride: func(string) (map[string]bool, bool) { return nil, false },
		CanConnectLocalPort:       func(int) bool { return false },
		CanReachMCPEndpoint:       func(int) bool { return false },
		MCPEndpoint: func(result configpkg.OpenResult) string {
			return fmt.Sprintf("http://127.0.0.1:%d/mcp", localports.MCPPortForResult(result))
		},
		MCPBearer: func(string, string) string { return "" },
		LoadIdleStatus: func(context.Context, string, string) (model.EnvironmentIdleStatus, error) {
			return model.EnvironmentIdleStatus{}, errors.New("idle status is not wired in this test")
		},
		LoadPodBranch: func(context.Context, string, string) (string, error) {
			return "", errors.New("pod branch is not wired in this test")
		},
		RunCommand: func(context.Context, string, string, ...string) (string, error) {
			return "", errors.New("host commands are not wired in this test")
		},
		ForgetForwardRepair:    func(uimodel.Selection) {},
		ReconcileForwardHealth: func(uimodel.Selection, int, bool) bool { return false },
		NoteEdgeAnswering:      func(uimodel.Selection) {},
		EffectiveContainerRegistries: func(string, string, configpkg.EnvConfig) (configpkg.ContainerRegistries, bool) {
			return configpkg.ContainerRegistries{}, false
		},
		LinkedCloudContextFor: func(string, string) (model.CloudContextStatus, bool, error) {
			return model.CloudContextStatus{}, false, nil
		},
		RuntimeRegistryNamespace: func(string, string) string { return "" },
	}
}

type fakeStore struct {
	tenants map[string]model.TenantConfig
	envs    map[string]configpkg.EnvConfig
}

func (s fakeStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return configpkg.ERunConfig{}, "", nil
}

func (s fakeStore) LoadTenantConfig(name string) (model.TenantConfig, string, error) {
	config, ok := s.tenants[name]
	if !ok {
		return model.TenantConfig{}, "", configpkg.ErrNotInitialized
	}
	return config, "", nil
}

func (s fakeStore) LoadEnvConfig(tenant, environment string) (configpkg.EnvConfig, string, error) {
	config, ok := s.envs[tenant+"/"+environment]
	if !ok {
		return configpkg.EnvConfig{}, "", configpkg.ErrNotInitialized
	}
	return config, "", nil
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
