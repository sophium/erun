package envops

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/localports"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/command/lifecycle/stop"
	"github.com/sophium/erun/erun-common/command/workspace"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// newTestService fills every seam d leaves unset with an inert fake, so no test
// reaches a process, a cluster or a pod it did not wire.
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

func inertDeps() Deps {
	return Deps{
		Store:           fakeStore{},
		FindProjectRoot: func() (string, string, error) { return "", "", errors.New("no project in this test") },
		Context:         context.Background,
		Started:         func() bool { return false },
		Emit:            func(string, ...any) {},
		MCPEndpoint: func(result config.OpenResult) string {
			return fmt.Sprintf("http://127.0.0.1:%d/mcp", localports.MCPPortForResult(result))
		},
		MCPBearer:           func(string, string) string { return "" },
		CanConnectLocalPort: func(int) bool { return false },
		CanReachMCPEndpoint: func(int) bool { return false },
		WrapMCPUnreachable: func(kind mcp.LocalMCPUnreachableKind, err error) error {
			return unreachableError{kind: kind, err: err}
		},
		IsMCPDialFailure: func(err error) bool {
			return err != nil && strings.Contains(err.Error(), "connection refused")
		},
		ReconnectMCP: func(context.Context, config.OpenResult, func(string)) error {
			return errors.New("reconnect is not wired in this test")
		},
		EnsureSSHD: func(context.Context, config.OpenResult) error {
			return errors.New("sshd is not wired in this test")
		},
		ReconnectLineHandler: func(uimodel.Selection) func(string) { return func(string) {} },
		DeployInFlight:       func(uimodel.Selection) bool { return false },
		NoteEdgeAnswering:    func(uimodel.Selection) {},
		StopRuntime: func(core.Context, stop.StopEnvironmentParams) (model.StopEnvironmentResult, error) {
			return model.StopEnvironmentResult{}, errors.New("stop is not wired in this test")
		},
		ReadRuntimeRunState: func(core.Context, stop.RuntimeScaleTarget) (stop.RuntimeRunState, error) {
			return stop.RuntimeRunState{}, errors.New("the Kubernetes reader is not wired in this test")
		},
		ResolveImageRegistry: func(context.Context, string, string) (model.RuntimeRegistryVersions, error) {
			return model.RuntimeRegistryVersions{}, errors.New("image registry is not wired in this test")
		},
		LoadJobs: func(context.Context, string, string) ([]jobstate.EnvironmentJob, error) {
			return nil, errors.New("pod jobs are not wired in this test")
		},
		ReadJobOutput: func(context.Context, string, string, model.ReadEnvironmentJobOutputParams) (jobs.EnvironmentJobOutput, error) {
			return jobs.EnvironmentJobOutput{}, errors.New("pod job output is not wired in this test")
		},
		CancelJob: func(context.Context, string, string, model.CancelEnvironmentJobParams) (jobs.CancelEnvironmentJobResult, error) {
			return jobs.CancelEnvironmentJobResult{}, errors.New("pod job cancel is not wired in this test")
		},
		ExecCommit: func(context.Context, string, string, string, string) (model.CommitWorkingTreeResult, error) {
			return model.CommitWorkingTreeResult{}, errors.New("exec commit is not wired in this test")
		},
		ExecPush: func(context.Context, string, string, string, string) (model.PushWorkingTreeBranchResult, error) {
			return model.PushWorkingTreeBranchResult{}, errors.New("exec push is not wired in this test")
		},
		ListAgentOutputs: func(config.OpenResult, model.RuntimeOutputsParams) (model.RuntimeOutputsListResult, error) {
			return model.RuntimeOutputsListResult{}, errors.New("agent outputs are not wired in this test")
		},
		DownloadAgentOutput: func(config.OpenResult, model.RuntimeOutputDownloadParams) (model.RuntimeOutputResult, error) {
			return model.RuntimeOutputResult{}, errors.New("agent output download is not wired in this test")
		},
		LaunchHostOpener:   func(string, []string) error { return errors.New("host opener is not wired in this test") },
		LaunchHostArtifact: func(string, string) error { return errors.New("host artifact launch is not wired in this test") },
		OpenHostIDE:        func(string, string) error { return errors.New("host IDE is not wired in this test") },
		WorkspaceSyncReady: func(context.Context, string) error {
			return errors.New("workspace sync is not wired in this test")
		},
		SyncWorkspace: func(context.Context, workspace.WorkspaceSyncParams) (model.WorkspaceSyncResult, error) {
			return model.WorkspaceSyncResult{}, errors.New("workspace sync is not wired in this test")
		},
		WorkspaceSyncInterval: time.Hour,
		ReviewPlatform: func(string) (string, *platformclient.PlatformClient, error) {
			return "", nil, errors.New("the platform is not wired in this test")
		},
		RestrictedRead: func(model.PlatformCapabilities, string) string { return "" },
		QueuePosition:  func([]model.PlatformReview, string) int { return 0 },
		Review:         ReviewPolicy{RequestTimeout: time.Second},
	}
}

// fakeStore's maps are shared by its copies, so a test can change a config
// after handing the store over.
type fakeStore struct {
	tenants map[string]model.TenantConfig
	envs    map[string]config.EnvConfig
}

func (s fakeStore) LoadERunConfig() (config.ERunConfig, string, error) {
	return config.ERunConfig{}, "", nil
}

func (s fakeStore) LoadTenantConfig(name string) (model.TenantConfig, string, error) {
	tenant, ok := s.tenants[name]
	if !ok {
		return model.TenantConfig{}, "", config.ErrNotInitialized
	}
	return tenant, "", nil
}

func (s fakeStore) LoadEnvConfig(tenant, environment string) (config.EnvConfig, string, error) {
	env, ok := s.envs[tenant+"/"+environment]
	if !ok {
		return config.EnvConfig{}, "", config.ErrNotInitialized
	}
	return env, "", nil
}

func (s fakeStore) SaveEnvConfig(tenant string, env config.EnvConfig) error {
	s.envs[tenant+"/"+env.Name] = env
	return nil
}

func (s fakeStore) ListTenantConfigs() ([]model.TenantConfig, error) {
	tenants := make([]model.TenantConfig, 0, len(s.tenants))
	for _, tenant := range s.tenants {
		tenants = append(tenants, tenant)
	}
	return tenants, nil
}

func (s fakeStore) ListEnvConfigs(tenant string) ([]config.EnvConfig, error) {
	envs := make([]config.EnvConfig, 0)
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

func envStatuses(emits *capturedEmits) []uimodel.EnvStatusPayload {
	var out []uimodel.EnvStatusPayload
	for _, raw := range emits.events(uimodel.EnvStatusEvent) {
		if payload, ok := raw.(uimodel.EnvStatusPayload); ok {
			out = append(out, payload)
		}
	}
	return out
}

func notifications(emits *capturedEmits) []uimodel.AppNotificationPayload {
	var out []uimodel.AppNotificationPayload
	for _, raw := range emits.events(uimodel.AppNotificationEvent) {
		if payload, ok := raw.(uimodel.AppNotificationPayload); ok {
			out = append(out, payload)
		}
	}
	return out
}

func requireNoError(t *testing.T, err error, context string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", context, err)
	}
}
