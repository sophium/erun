package activity

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
	"github.com/sophium/erun/erun-common/base/model"
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

func inertDeps() Deps {
	return Deps{
		Store:           fakeStore{},
		FindProjectRoot: func() (string, string, error) { return "", "", errors.New("no project in this test") },
		Context:         context.Background,
		Started:         func() bool { return false },
		Emit:            func(string, ...any) {},
		Sessions:        newFakeSessions(),
		AppSessionEnv:   "ERUN_APP_SESSION=1",
		ResolveCLIPath:  func() string { return "erun-not-wired-in-this-test" },
		ResolveImageRegistry: func(context.Context, string, string) (model.RuntimeRegistryVersions, error) {
			return model.RuntimeRegistryVersions{}, errors.New("image registry is not wired in this test")
		},
		RuntimeRegistryNamespace: func(string, string) string { return "" },
		RuntimeChartPlan:         func(string, string, string, bool) uimodel.ChartPlan { return uimodel.ChartPlan{} },
		FetchContainerStatuses: func(context.Context, Entry) ([]ContainerStatus, error) {
			return nil, errors.New("container statuses are not wired in this test")
		},
		MCPEndpoint: func(result config.OpenResult) string {
			return fmt.Sprintf("http://127.0.0.1:%d/mcp", config.MCPPortForResult(result))
		},
		MCPBearer:           func(string, string) string { return "" },
		CanReachMCPEndpoint: func(int) bool { return false },
		RunPodRaw: func(context.Context, string, string, []string) (string, error) {
			return "", errors.New("pod exec is not wired in this test")
		},
		StartDir:                   func(string) string { return "." },
		AppendDeployComponents:     func(args []string, _ uimodel.Selection) []string { return args },
		RuntimeImageOverride:       func(uimodel.Selection) string { return "" },
		AppendMCPAuthPublicKeyFlag: func(args []string) []string { return args },
		EnvironmentDeployed:        func(string, string) {},
		EnvironmentInitialized:     func(string, string) {},
		EnvironmentInitFailed:      func(string, string) {},
		DoctorCompleted:            func(string, string, bool, string) {},
		SSHDInitCompleted:          func(string, string, bool, string) {},
		InterruptedActivityPath:    func() string { return "" },
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

// fakeSession is one registered terminal session as the fake registry holds it.
type fakeSession struct {
	Session
	closed   bool
	dead     bool
	lockedBy string
}

// fakeSessions is an in-memory terminal registry with the terminal's lock semantics.
type fakeSessions struct {
	mu       sync.Mutex
	sessions []*fakeSession
}

func newFakeSessions() *fakeSessions { return &fakeSessions{} }

func (f *fakeSessions) add(session Session) *fakeSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	registered := &fakeSession{Session: session}
	f.sessions = append(f.sessions, registered)
	return registered
}

func (f *fakeSessions) lockedBy(serial int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, session := range f.sessions {
		if session.Serial == serial {
			return session.lockedBy
		}
	}
	return ""
}

func (f *fakeSessions) LockMatching(id string, match func(Session) bool) []Session {
	f.mu.Lock()
	defer f.mu.Unlock()
	var locked []Session
	for _, session := range f.sessions {
		if session.closed || session.lockedBy == id || !match(session.Session) {
			continue
		}
		session.lockedBy = id
		locked = append(locked, session.Session)
	}
	return locked
}

func (f *fakeSessions) LockSerial(serial int, id string) (Session, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, session := range f.sessions {
		if session.closed || session.Serial != serial {
			continue
		}
		if session.lockedBy == id {
			return Session{}, false
		}
		session.lockedBy = id
		return session.Session, true
	}
	return Session{}, false
}

func (f *fakeSessions) Unlock(id string) []Session {
	f.mu.Lock()
	defer f.mu.Unlock()
	var unlocked []Session
	for _, session := range f.sessions {
		if session.lockedBy != id {
			continue
		}
		session.lockedBy = ""
		unlocked = append(unlocked, session.Session)
	}
	return unlocked
}

func (f *fakeSessions) DeadSessions(minAge time.Duration) []Session {
	f.mu.Lock()
	defer f.mu.Unlock()
	var dead []Session
	for _, session := range f.sessions {
		if session.closed || !session.dead || time.Since(session.StartedAt) < minAge {
			continue
		}
		dead = append(dead, session.Session)
	}
	return dead
}

func (f *fakeSessions) Kill(serial int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, session := range f.sessions {
		if session.Serial == serial {
			session.closed = true
			return true
		}
	}
	return false
}

func (f *fakeSessions) OpenSelections() []uimodel.Selection {
	f.mu.Lock()
	defer f.mu.Unlock()
	var selections []uimodel.Selection
	for _, session := range f.sessions {
		if !session.closed {
			selections = append(selections, session.Selection)
		}
	}
	return selections
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

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// harness is a service over a fake terminal registry that records every emit.
type harness struct {
	*Service
	sessions *fakeSessions
	emits    *capturedEmits
}

func newHarness(d Deps) *harness {
	h := &harness{sessions: newFakeSessions(), emits: newCapturedEmits()}
	if d.Sessions == nil {
		d.Sessions = h.sessions
	}
	if d.Emit == nil {
		d.Emit = h.emits.fn()
	}
	h.Service = newTestService(d)
	return h
}
