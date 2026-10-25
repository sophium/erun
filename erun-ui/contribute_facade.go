package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	activitysvc "github.com/sophium/erun/erun-ui/internal/activity"
	"github.com/sophium/erun/erun-ui/internal/contribute"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// The contribute mode surface; the behavior lives in internal/contribute.

func (a *App) IsContributeEligible(selection uimodel.Selection) bool {
	return a.contribute.IsEligible(selection)
}

func (a *App) GetContributeMode(selection uimodel.Selection) bool {
	return a.contribute.Mode(selection)
}

func (a *App) SetContributeMode(selection uimodel.Selection, on bool) (contribute.State, error) {
	return a.contribute.SetMode(selection, on)
}

// StartContributeApp opens the app through the Wails runtime because
// window.open stays inside the WKWebView.
func (a *App) StartContributeApp(selection uimodel.Selection) (contribute.AppLaunch, error) {
	launch, err := a.contribute.StartApp(selection)
	if err == nil && a.ctx != nil {
		runtime.BrowserOpenURL(a.ctx, launch.URL)
	}
	return launch, err
}

func (a *App) StartContributeSession(selection uimodel.Selection, slot, cols, rows int) (uimodel.StartSessionResult, error) {
	return a.startContributeSession("start contribute ERun session", activitysvc.GatedContributeERun, selection, slot, cols, rows, false)
}

func (a *App) StartContributeAISession(selection uimodel.Selection, slot, cols, rows int) (uimodel.StartSessionResult, error) {
	return a.startContributeSession("start contribute AI session", activitysvc.GatedContributeAI, selection, slot, cols, rows, true)
}

// startContributeSession runs the start through the env's action runner, which
// waits on the started tab before admitting the next action.
func (a *App) startContributeSession(action, gated string, selection uimodel.Selection, slot, cols, rows int, withAI bool) (uimodel.StartSessionResult, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment(action, selection.Tenant, selection.Environment); err != nil {
		return uimodel.StartSessionResult{}, err
	}
	return a.activity.EnqueueGatedSession(selection, gated, func(context.Context) (uimodel.StartSessionResult, activitysvc.Readiness, error) {
		return a.contribute.RunSession(selection, slot, cols, rows, withAI)
	})
}

// newContributeService reads the deps funcs per call, so a test that swaps a
// dep after NewApp still reaches its fake.
func (a *App) newContributeService() *contribute.Service {
	return contribute.New(contribute.Deps{
		Store:       a.deps.store,
		Context:     a.context,
		Emit:        a.emit,
		Sessions:    contributeSessions{a: a},
		MCPEndpoint: mcpEndpointForOpenResult,
		MCPBearer:   a.mcpBearer,
		// A missing probe reads as reachable, so the clone is still attempted.
		CanReachMCPEndpoint: func(port int) bool {
			return a.deps.canReachMCPEndpoint == nil || a.deps.canReachMCPEndpoint(port)
		},
		WrapMCPUnreachable: wrapMCPUnreachableError,
		IsMCPDialFailure:   isMCPDialFailure,
		CloneERun: func(ctx context.Context, endpoint, bearer string) error {
			if a.deps.cloneERun == nil {
				return errors.New("contribute clone is not configured")
			}
			return a.deps.cloneERun(ctx, endpoint, bearer)
		},
		StatePath: a.deps.contributeStatePath,
	})
}

// contributeSessions is the terminal registry as the contribute service reaches it.
type contributeSessions struct {
	a *App
}

func (s contributeSessions) Reuse(key string) (contribute.Reused, bool) {
	var existing *terminal.Session
	s.a.terminal.WithLock(func(l *terminal.Locked) {
		if candidate := l.Session(key); candidate != nil && !candidate.Closed && candidate.PTY != nil {
			existing = candidate
		}
	})
	if existing == nil {
		return contribute.Reused{}, false
	}
	existing.SignalReady(nil)
	return contribute.Reused{Serial: existing.Serial, Selection: existing.Selection, Ready: existing}, true
}

func (s contributeSessions) Spawn(launch contribute.Launch) (int, contribute.Readiness, error) {
	a := s.a
	params := terminal.StartParams{
		Dir:        terminal.ResolveStartDir(launch.RepoPath),
		Executable: a.deps.resolveCLIPath(),
		Args:       terminal.WithAppSession(terminal.BuildOpenArgs(launch.Tenant, launch.Environment), launch.AppSession, launch.WithAI, true),
		Env:        []string{appSessionEnvVar + "=1"},
		Cols:       launch.Cols,
		Rows:       launch.Rows,
	}
	session, err := a.deps.startTerminal(params)
	if err != nil {
		return 0, nil, err
	}

	selection := launch.Selection
	var managed *terminal.Session
	a.terminal.WithLock(func(l *terminal.Locked) {
		managed = &terminal.Session{
			PTY:                    session,
			Selection:              selection,
			Key:                    launch.Key,
			Serial:                 l.NextSerial(),
			Slot:                   launch.Slot,
			Kind:                   terminal.Kind(launch.Kind),
			AppSession:             launch.AppSession,
			BlocksIdleStop:         true,
			ClearIdleBlockOnOutput: true,
			Respawn: func() (terminal.PTY, error) {
				return a.deps.startTerminal(terminal.ReconnectParams(params))
			},
			StartedAt: time.Now(),
			LastCols:  launch.Cols,
			LastRows:  launch.Rows,
		}
		l.Register(managed)
		l.AddBusy(selection)
	})

	a.terminal.RecordActivity(selection)
	a.activity.RememberKubeContext(selection.KubernetesContext)
	go a.terminal.Stream(managed)
	go a.envops.StartWorkspaceSync(selection)
	go a.cloudContexts.StartRefresher(selection)
	a.terminal.LogSpawnedCommandToLocal(selection, launch.Kind, terminal.FormatLocalCommandLog(terminal.FormatLaunchCommand(params), launch.TabName))
	return managed.Serial, managed, nil
}

func (s contributeSessions) CloseWithKeyPrefix(prefixes ...string) {
	s.a.terminal.WithLock(func(l *terminal.Locked) {
		for key, managed := range l.Sessions() {
			if managed == nil || !hasAnyPrefix(key, prefixes) {
				continue
			}
			_ = managed.PTY.Close()
			l.Delete(key)
		}
	})
}

func (s contributeSessions) WriteToFirstWithKeyPrefix(prefix, input string) {
	var target *terminal.Session
	s.a.terminal.WithLock(func(l *terminal.Locked) {
		for key, managed := range l.Sessions() {
			if managed == nil || managed.Closed || managed.PTY == nil {
				continue
			}
			if strings.HasPrefix(key, prefix) {
				target = managed
				break
			}
		}
	})
	if target == nil {
		return
	}
	_, _ = io.WriteString(target.PTY, input)
}

func hasAnyPrefix(key string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}
