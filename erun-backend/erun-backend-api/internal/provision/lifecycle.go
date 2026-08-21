package provision

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/deployexec"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

// UsageRecorder records a per-tenant metering event (#605). Optional: a nil
// recorder simply records nothing.
type UsageRecorder interface {
	Record(ctx context.Context, event model.UsageEvent) error
}

// EnvLifecycleRunner runs a hosted env's stop/delete Job to a terminal result —
// satisfied by the deployexec Launcher.
type EnvLifecycleRunner interface {
	RunStop(ctx context.Context, params deployexec.StopJobParams) (deployexec.Result, error)
	RunDelete(ctx context.Context, params deployexec.DeleteJobParams) (deployexec.Result, error)
}

// EnvironmentRowDeleter hard-deletes an environment's row once its namespace
// (if any) is confirmed torn down.
type EnvironmentRowDeleter interface {
	Delete(ctx context.Context, environmentID string) error
}

// EnvLifecycleInput is the non-secret placement a stop or delete Job needs:
// the same coordinates a deploy Job uses, without a target version — stop and
// delete act on whatever the environment is already running.
type EnvLifecycleInput struct {
	Tenant        string
	Environment   string
	EnvironmentID string
	// RunningVersion picks the runtime image the Job runs erun from: the last
	// version this environment actually deployed. Empty means the environment
	// never successfully deployed, so there is no namespace to touch.
	RunningVersion string
}

// EnvLifecycle runs a hosted env's stop/delete synchronously, unlike
// EnvProvisioner's durable DBOS workflow: the underlying Jobs are short
// kubectl operations (scale to zero, delete namespace), not a multi-minute
// helm rollout, so a blocking HTTP handler with a bounded request context is
// an acceptable v1 shape. Revisit if a tenant's teardown routinely runs long
// (stuck namespace finalizers, for example).
type EnvLifecycle struct {
	runner       EnvLifecycleRunner
	rows         EnvironmentRowDeleter
	config       EnvDeployConfig
	usage        UsageRecorder
	imageChecker RuntimeImageChecker
}

// NewEnvLifecycle wires stop/delete. usage may be nil, which records no
// metering event. imageChecker may be nil, which skips the published-image
// fallback and always names the tenant's own image.
func NewEnvLifecycle(runner EnvLifecycleRunner, rows EnvironmentRowDeleter, config EnvDeployConfig, usage UsageRecorder, imageChecker RuntimeImageChecker) *EnvLifecycle {
	return &EnvLifecycle{runner: runner, rows: rows, config: config, usage: usage, imageChecker: imageChecker}
}

func (l *EnvLifecycle) recordUsage(ctx context.Context, environmentID string, eventType model.UsageEventType) {
	if l.usage == nil {
		return
	}
	if err := l.usage.Record(ctx, model.UsageEvent{EnvironmentID: environmentID, EventType: string(eventType)}); err != nil {
		log.Printf("erun api env lifecycle: recording usage event for environment=%q did not persist: %v", environmentID, err)
	}
}

// image resolves the runtime image a stop/delete Job runs erun from, applying
// the same tenant-image-with-published-fallback decision the deploy Job uses
// (ResolveRuntimeImage) — an environment that was bootstrapped onto the
// canonical erun-devops image at deploy time is still running it, so its stop
// and delete Jobs must name the same image rather than the tenant's own image
// that was already confirmed missing.
func (l *EnvLifecycle) image(ctx context.Context, tenant, version string) string {
	image, _ := ResolveRuntimeImage(ctx, l.imageChecker, l.config.Registry, tenant, version)
	return image
}

// Stop scales the environment's runtime Deployment to zero. It does not touch
// the environment's provisioning-lifecycle status: a stopped runtime
// environment stays "running" — paused, not torn down.
func (l *EnvLifecycle) Stop(ctx context.Context, input EnvLifecycleInput) error {
	if strings.TrimSpace(input.RunningVersion) == "" {
		return fmt.Errorf("environment has never been deployed; nothing to stop")
	}
	result, err := l.runner.RunStop(ctx, deployexec.StopJobParams{
		Tenant:         input.Tenant,
		Environment:    input.Environment,
		Namespace:      l.config.PlatformNamespace,
		Image:          l.image(ctx, input.Tenant, input.RunningVersion),
		ServiceAccount: l.config.DeployerServiceAccount,
	})
	if err != nil {
		return err
	}
	if result.Outcome != deployexec.OutcomeSucceeded {
		return fmt.Errorf("stop job %s: %s", result.Outcome, lifecycleFailureDetail(result))
	}
	l.recordUsage(ctx, input.EnvironmentID, model.UsageEventEnvironmentStopped)
	return nil
}

// Delete tears down the environment's namespace (skipped when it never
// deployed, since there is nothing to tear down) and, only once that
// succeeds, removes its row. A failed teardown must not silently forget an
// environment whose namespace may still exist.
func (l *EnvLifecycle) Delete(ctx context.Context, input EnvLifecycleInput) error {
	if strings.TrimSpace(input.RunningVersion) != "" {
		result, err := l.runner.RunDelete(ctx, deployexec.DeleteJobParams{
			Tenant:                  input.Tenant,
			Environment:             input.Environment,
			Namespace:               l.config.PlatformNamespace,
			Image:                   l.image(ctx, input.Tenant, input.RunningVersion),
			ServiceAccount:          l.config.DeployerServiceAccount,
			ExposeServicesZone:      l.config.ExposeServicesZone,
			ExposePlatformNamespace: l.config.ExposePlatformNamespace,
		})
		if err != nil {
			return err
		}
		if result.Outcome != deployexec.OutcomeSucceeded {
			return fmt.Errorf("delete job %s: %s", result.Outcome, lifecycleFailureDetail(result))
		}
		// The environment row is about to be removed, so a failed DNS cleanup
		// (best-effort, #1094) has nowhere to be recorded once this returns —
		// the server log is the only place left to name it.
		if reason := deployexec.UnexposeFailureFromOutput(result.Output); reason != "" {
			log.Printf("erun api env lifecycle: dns cleanup for %s/%s did not succeed: %s", input.Tenant, input.Environment, reason)
		}
	}
	l.recordUsage(ctx, input.EnvironmentID, model.UsageEventEnvironmentDeleted)
	return l.rows.Delete(ctx, input.EnvironmentID)
}

func lifecycleFailureDetail(result deployexec.Result) string {
	if detail := strings.TrimSpace(result.Failure); detail != "" {
		return detail
	}
	return "no reason recorded (its pod was already reclaimed)"
}
