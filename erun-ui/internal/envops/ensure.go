package envops

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/deploy/kubecheck"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// ensureTTL lets one reconnect cover the burst of spawns an env (re)start produces.
const ensureTTL = 30 * time.Second

// EnsureReason: a tab spawn defers to a recent success; a forward repair or a
// failed edge probe has evidence that outranks it.
type EnsureReason int

const (
	EnsureForTabSpawn EnsureReason = iota
	EnsureForBrokenForward
)

// EnsureRuntimeOnce rebinds MCP/API forwarders at most once per (re)start window. It never deploys.
func (s *Service) EnsureRuntimeOnce(selection uimodel.Selection) {
	s.ensureRuntime(selection, EnsureForTabSpawn)
}

// ensureRun is one rebind in flight; reached is written before settled closes, so waiters read it lock-free.
type ensureRun struct {
	settled chan struct{}
	reached bool
}

// claimEnsure takes the per-env latch. In-flight runs block every caller;
// the completed window only covers callers without evidence of a fault. A refused
// caller gets the running run back; mine reports whether this call performs it.
func (s *Service) claimEnsure(key string, reason EnsureReason) (run *ensureRun, mine bool) {
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()
	if inflight, ok := s.ensureInflight[key]; ok {
		return inflight, false
	}
	if done, ok := s.ensureDone[key]; ok && reason == EnsureForTabSpawn && time.Since(done) < ensureTTL {
		return nil, false
	}
	run = &ensureRun{settled: make(chan struct{})}
	s.ensureInflight[key] = run
	return run, true
}

// ensureRuntime starts a rebind and reports whether this call started it. A failed
// reconnect is surfaced and does not stamp the success window, so the next open retries.
func (s *Service) ensureRuntime(selection uimodel.Selection, reason EnsureReason) bool {
	selection, run, mine := s.beginEnsure(selection, reason)
	if !mine {
		return false
	}
	go s.runEnsure(selection, uimodel.SelectionKey(selection), run)
	return true
}

// EnsureRuntimeWithin waits up to budget for the rebind, for callers that next hand
// a client this port. Timing out is not a verdict; the rebind keeps running.
func (s *Service) EnsureRuntimeWithin(selection uimodel.Selection, reason EnsureReason, budget time.Duration) bool {
	selection, run, mine := s.beginEnsure(selection, reason)
	if run == nil {
		return false
	}
	if mine {
		go s.runEnsure(selection, uimodel.SelectionKey(selection), run)
	}
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case <-run.settled:
		return run.reached
	case <-timer.C:
		return false
	}
}

// EnsureInFlight reports whether a rebind for the environment is running.
func (s *Service) EnsureInFlight(selection uimodel.Selection) bool {
	key := uimodel.SelectionKey(selection)
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()
	_, inflight := s.ensureInflight[key]
	return inflight
}

// EnsureReachedAt reports when a rebind last reached the environment's runtime.
func (s *Service) EnsureReachedAt(selection uimodel.Selection) (time.Time, bool) {
	key := uimodel.SelectionKey(selection)
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()
	done, ok := s.ensureDone[key]
	return done, ok
}

// beginEnsure returns a nil run when the window already confirmed reach or
// startup has not run (unit tests), so tests never fall back to the real CLI.
func (s *Service) beginEnsure(selection uimodel.Selection, reason EnsureReason) (uimodel.Selection, *ensureRun, bool) {
	if !s.d.Started() {
		return selection, nil, false
	}
	selection = uimodel.NormalizeSelection(selection)
	run, mine := s.claimEnsure(uimodel.SelectionKey(selection), reason)
	return selection, run, mine
}

// runEnsure is the run's only writer, so settling needs no extra lock.
func (s *Service) runEnsure(selection uimodel.Selection, key string, run *ensureRun) {
	var ensureErr error
	defer func() {
		s.ensureMu.Lock()
		delete(s.ensureInflight, key)
		reached := ensureErr == nil
		if reached {
			s.ensureDone[key] = time.Now()
			// End the failure episode so a later failure notifies again.
			delete(s.ensureFailNotified, key)
		}
		run.reached = reached
		close(run.settled)
		s.ensureMu.Unlock()
		if reached {
			s.clearEnvNotification(selection.Tenant, selection.Environment, "")
		}
	}()
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		ensureErr = err
		s.surfaceEnsureFailure(selection, err)
		return
	}
	onLine := s.d.ReconnectLineHandler(selection)
	if ensureErr = s.d.ReconnectMCP(context.Background(), result, onLine); ensureErr != nil {
		s.surfaceEnsureFailure(selection, ensureErr)
	}
}

// surfaceEnsureFailure reports a failed reconnect. A failure does not mean
// the runtime is down; only a confirmed absent deployment offers the deploy action.
func (s *Service) surfaceEnsureFailure(selection uimodel.Selection, err error) {
	selection = uimodel.NormalizeSelection(selection)
	// An in-flight deploy is already the recovery, and its overlay says so.
	if s.d.DeployInFlight(selection) {
		return
	}
	// `open --reconnect` refuses to start a stopped runtime; that is not an outage.
	if s.RuntimeStopped(selection) {
		s.surfaceRuntimeStopped(selection)
		return
	}
	// The row status updates every attempt; the notification posts once per failure episode.
	s.emitEnvStatus(selection, uimodel.EnvStatusFailed)
	if s.ensureFailureAlreadyNotified(selection) {
		return
	}
	// Kind "warning" (not "warn") is what the frontend maps to the attention icon.
	if runtimeCheckFoundDeploymentAbsent(err) {
		s.notifyEnv("warning", selection.Tenant, selection.Environment, SourceRuntimeUnreachable, fmt.Sprintf(
			"Could not reach the runtime for %s/%s: %s. Deploy the environment to bring it up.",
			selection.Tenant, selection.Environment, strings.TrimSpace(err.Error()),
		), uimodel.NotificationActionDeploy)
		return
	}
	// Absence is not confirmed, so no deploy is offered; it could roll a healthy runtime.
	s.notifyEnv("warning", selection.Tenant, selection.Environment, SourceRuntimeUnreachable, fmt.Sprintf(
		"Could not tell whether the runtime for %s/%s is up: %s. Check the cluster connection, credentials, and permissions for this environment.",
		selection.Tenant, selection.Environment, strings.TrimSpace(err.Error()),
	), "")
}

// runtimeCheckFoundDeploymentAbsent matches only ensureRuntimeDeployed's
// "runtime is not deployed" error; any other failure is not evidence of absence.
func runtimeCheckFoundDeploymentAbsent(err error) bool {
	return err != nil && strings.Contains(err.Error(), kubecheck.KubernetesDeploymentAbsentMessageMarker)
}

// surfaceRuntimeStopped shows a stopped env as stopped and names the way back.
func (s *Service) surfaceRuntimeStopped(selection uimodel.Selection) {
	s.emitEnvStatus(selection, uimodel.EnvStatusRuntimeStopped)
	if s.ensureFailureAlreadyNotified(selection) {
		return
	}
	s.notifyEnv("info", selection.Tenant, selection.Environment, SourceRuntimeUnreachable, fmt.Sprintf(
		"%s/%s is stopped, so its sessions did not reconnect. Open it to start it again.",
		selection.Tenant, selection.Environment,
	), "")
}

// ensureFailureAlreadyNotified latches one notification per failure episode.
func (s *Service) ensureFailureAlreadyNotified(selection uimodel.Selection) bool {
	key := uimodel.SelectionKey(selection)
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()
	_, alreadyNotified := s.ensureFailNotified[key]
	s.ensureFailNotified[key] = struct{}{}
	return alreadyNotified
}
