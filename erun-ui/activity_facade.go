package main

import (
	"context"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	activitysvc "github.com/sophium/erun/erun-ui/internal/activity"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// The activity queue surface; the behavior lives in internal/activity.

func (a *App) ApplyPinVersion(selection uimodel.Selection, target string) (activitysvc.PinPlan, error) {
	return a.activity.ApplyPinVersion(selection, target)
}

func (a *App) CancelWaitingAction(id string) bool {
	return a.activity.CancelWaitingAction(id)
}

func (a *App) ConsumeInterruptedActivityNotice() []activitysvc.Entry {
	return a.activity.ConsumeInterruptedActivityNotice()
}

func (a *App) DismissDeploy(id string) bool {
	return a.activity.DismissDeploy(id)
}

func (a *App) FindActiveDeployForSelection(selection uimodel.Selection) activitysvc.Entry {
	return a.activity.FindActiveDeployForSelection(selection)
}

func (a *App) ForceDismissActivity(id string) bool {
	return a.activity.ForceDismissActivity(id)
}

func (a *App) GetDeploy(id string) activitysvc.Entry {
	return a.activity.GetDeploy(id)
}

func (a *App) KillSession(serial int) bool {
	return a.activity.KillSession(serial)
}

func (a *App) ListDeploys() []activitysvc.Entry {
	return a.activity.ListDeploys()
}

func (a *App) ListPinnableVersions(selection uimodel.Selection) ([]string, error) {
	return a.activity.ListPinnableVersions(selection)
}

func (a *App) LoadDeployComponents(selection uimodel.Selection) (activitysvc.DeployComponents, error) {
	return a.activity.LoadDeployComponents(selection)
}

func (a *App) LoadEnvTrace(selection uimodel.Selection) (activitysvc.EnvTrace, error) {
	return a.activity.LoadEnvTrace(selection)
}

func (a *App) PinRepoCheckoutStatus(selection uimodel.Selection) (activitysvc.PinCheckoutStatus, error) {
	return a.activity.PinRepoCheckoutStatus(selection)
}

func (a *App) PreviewPinVersion(selection uimodel.Selection, target string) (activitysvc.PinPlan, error) {
	return a.activity.PreviewPinVersion(selection, target)
}

func (a *App) RecoverPendingHelmRelease(id string) activitysvc.RecoveryResult {
	return a.activity.RecoverPendingHelmRelease(id)
}

func (a *App) RevertPinVersion(selection uimodel.Selection) (activitysvc.PinPlan, error) {
	return a.activity.RevertPinVersion(selection)
}

// newActivityService reads the deps funcs and sibling services per call, so a
// test that builds App by hand or swaps a dep after NewApp still reaches its fake.
func (a *App) newActivityService() *activitysvc.Service {
	return activitysvc.New(activitysvc.Deps{
		Store:           a.deps.store,
		FindProjectRoot: func() (string, string, error) { return a.deps.findProjectRoot() },
		Context:         a.context,
		Started:         func() bool { return a.ctx != nil },
		Emit:            a.emit,
		Sessions:        activitySessions{a: a},
		AppSessionEnv:   appSessionEnvVar + "=1",
		ResolveCLIPath:  func() string { return a.deps.resolveCLIPath() },
		ResolveImageRegistry: func(ctx context.Context, namespace, repository string) (model.RuntimeRegistryVersions, error) {
			return a.deps.resolveImageRegistry(ctx, namespace, repository)
		},
		RuntimeRegistryNamespace: a.runtimeRegistryNamespace,
		RuntimeChartPlan: func(tenant, environment, version string, runtimeIsLocalChart bool) uimodel.ChartPlan {
			return a.runtime.ResolveRuntimeChartPlan(tenant, environment, version, runtimeIsLocalChart)
		},
		FetchContainerStatuses: activitysvc.FetchContainerStatusesViaKubectl,
		MCPEndpoint:            mcpEndpointForOpenResult,
		MCPBearer:              a.mcpBearer,
		CanReachMCPEndpoint:    func(port int) bool { return a.deps.canReachMCPEndpoint(port) },
		RunPodRaw: func(ctx context.Context, endpoint, bearer string, command []string) (string, error) {
			return a.deps.runPodRaw(ctx, endpoint, bearer, command)
		},
		StartDir:                   terminal.ResolveStartDir,
		AppendDeployComponents:     terminal.AppendDeployComponentsFlag,
		RuntimeImageOverride:       terminal.DeployRuntimeImageOverride,
		AppendMCPAuthPublicKeyFlag: a.appendMCPAuthPublicKeyFlag,
		EnvironmentDeployed:        func(tenant, environment string) { a.envops.EnvironmentDeployed(tenant, environment) },
		EnvironmentInitialized:     func(tenant, environment string) { a.envops.EnvironmentInitialized(tenant, environment) },
		EnvironmentInitFailed:      func(tenant, environment string) { a.envops.EnvironmentInitFailed(tenant, environment) },
		DoctorCompleted: func(tenant, environment string, success bool, message string) {
			a.envops.DoctorCompleted(tenant, environment, success, message)
		},
		SSHDInitCompleted: func(tenant, environment string, success bool, message string) {
			a.envops.SSHDInitCompleted(tenant, environment, success, message)
		},
		InterruptedActivityPath: func() string { return a.deps.interruptedActivityPath },
	})
}

// feedActivityTraceFromTerminal buffers a session's output under the terminal
// lock and hands its complete lines to the activity trace.
func (a *App) feedActivityTraceFromTerminal(managed *terminal.Session, chunk []byte) {
	if managed == nil || !activitysvc.FeedsTrace(managed.Selection, string(managed.Kind)) {
		return
	}
	var lines []string
	a.terminal.WithLock(func(*terminal.Locked) {
		managed.ActivityTraceBuffer, lines = activitysvc.DrainTraceLines(managed.ActivityTraceBuffer + activitysvc.StripTraceANSI(string(chunk)))
	})
	if a.activity.FeedTrace(managed.Selection, string(managed.Kind), lines, managed.SignalReady) {
		a.terminal.MarkTakenOver(managed)
	}
}

// gatedStart hands the runner's gate the session a terminal start returns; a nil
// session gates nothing.
func gatedStart(start func(context.Context) (uimodel.StartSessionResult, *terminal.Session, error)) activitysvc.GatedStart {
	return func(ctx context.Context) (uimodel.StartSessionResult, activitysvc.Readiness, error) {
		result, managed, err := start(ctx)
		if managed == nil {
			return result, nil, err
		}
		return result, managed, err
	}
}

// activitySessions is the terminal registry as the activity service reaches it.
type activitySessions struct {
	a *App
}

func activitySessionOf(managed *terminal.Session) activitysvc.Session {
	return activitysvc.Session{
		Serial:    managed.Serial,
		Selection: managed.Selection,
		Kind:      string(managed.Kind),
		StartedAt: managed.StartedAt,
	}
}

func (s activitySessions) LockMatching(id string, match func(activitysvc.Session) bool) []activitysvc.Session {
	var locked []activitysvc.Session
	s.a.terminal.WithLock(func(l *terminal.Locked) {
		for _, managed := range l.Sessions() {
			if managed == nil || managed.Closed || managed.LockedByActivity == id {
				continue
			}
			session := activitySessionOf(managed)
			if !match(session) {
				continue
			}
			managed.LockedByActivity = id
			locked = append(locked, session)
		}
	})
	return locked
}

func (s activitySessions) LockSerial(serial int, id string) (activitysvc.Session, bool) {
	var session activitysvc.Session
	locked := false
	s.a.terminal.WithLock(func(l *terminal.Locked) {
		for _, managed := range l.Sessions() {
			if managed == nil || managed.Closed || managed.Serial != serial {
				continue
			}
			if managed.LockedByActivity != id {
				managed.LockedByActivity = id
				session, locked = activitySessionOf(managed), true
			}
			return
		}
	})
	return session, locked
}

func (s activitySessions) Unlock(id string) []activitysvc.Session {
	var unlocked []activitysvc.Session
	s.a.terminal.WithLock(func(l *terminal.Locked) {
		for _, managed := range l.Sessions() {
			if managed == nil || managed.LockedByActivity != id {
				continue
			}
			managed.LockedByActivity = ""
			unlocked = append(unlocked, activitySessionOf(managed))
		}
	})
	return unlocked
}

// DeadSessions holds the terminal lock only briefly, so the detector cannot
// deadlock against terminal startup.
func (s activitySessions) DeadSessions(minAge time.Duration) []activitysvc.Session {
	now := time.Now()
	var dead []activitysvc.Session
	s.a.terminal.WithLock(func(l *terminal.Locked) {
		for _, managed := range l.Sessions() {
			if managed == nil || managed.Closed || managed.PTY == nil {
				continue
			}
			if managed.StartedAt.IsZero() || now.Sub(managed.StartedAt) < minAge {
				continue
			}
			pid := managed.PTY.Pid()
			// Ask the session, not the PID: EDR on locked-down Windows denies process probes.
			if pid <= 0 || managed.PTY.Alive() {
				continue
			}
			session := activitySessionOf(managed)
			session.Pid = pid
			dead = append(dead, session)
		}
	})
	return dead
}

// Kill marks the session closed before closing its PTY, so the stale detector
// does not re-surface the reaped process.
func (s activitySessions) Kill(serial int) bool {
	var managed *terminal.Session
	var pty terminal.PTY
	s.a.terminal.WithLock(func(l *terminal.Locked) {
		for _, candidate := range l.Sessions() {
			if candidate != nil && candidate.Serial == serial {
				managed = candidate
				break
			}
		}
		if managed != nil {
			pty = l.MarkClosed(managed)
		}
	})
	if managed == nil {
		return false
	}
	if pty != nil {
		_ = pty.Close()
	}
	return true
}

func (s activitySessions) OpenSelections() []uimodel.Selection {
	var selections []uimodel.Selection
	s.a.terminal.WithLock(func(l *terminal.Locked) {
		for _, managed := range l.Sessions() {
			if managed != nil && !managed.Closed {
				selections = append(selections, managed.Selection)
			}
		}
	})
	return selections
}
