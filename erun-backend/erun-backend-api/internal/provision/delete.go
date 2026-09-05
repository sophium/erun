package provision

import (
	"context"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// EnvDeleteInput is the durable delete workflow's checkpointed input: the
// non-secret twin of EnvLifecycleInput plus the identity a resumed or
// reconciler-driven attempt needs to rebind its status writes to the right
// tenant, and DeleteID, the attempt id that keys the workflow.
type EnvDeleteInput struct {
	TenantID      string `json:"tenantId"`
	TenantType    string `json:"tenantType"`
	ErunUserID    string `json:"erunUserId,omitempty"`
	EnvironmentID string `json:"environmentId"`
	Tenant        string `json:"tenant"`
	Environment   string `json:"environment"`
	// RunningVersion mirrors EnvLifecycleInput.RunningVersion: empty means the
	// environment never successfully deployed, so there is no namespace to
	// tear down.
	RunningVersion             string `json:"runningVersion,omitempty"`
	ContextID                  string `json:"contextId,omitempty"`
	PlacementKubernetesContext string `json:"placementKubernetesContext,omitempty"`
	PlacementServerURL         string `json:"placementServerUrl,omitempty"`
	// DeleteID identifies one explicit delete attempt. Being part of the
	// checkpointed input is what lets a resumed workflow rebuild the same Job
	// name and re-watch its own run, and — like EnvProvisionInput.DeployID —
	// is why the workflow is keyed by the attempt rather than the
	// environment: an environment-keyed id would replay a completed (e.g.
	// deletion-blocked) attempt's cached result on retry instead of actually
	// running again, the same replay bug root AGENTS.md's "Server-Side
	// Executors" section warns against for Job names.
	DeleteID string `json:"deleteId"`
}

func (input EnvDeleteInput) lifecycleInput() EnvLifecycleInput {
	return EnvLifecycleInput{
		Tenant:                     input.Tenant,
		Environment:                input.Environment,
		EnvironmentID:              input.EnvironmentID,
		RunningVersion:             input.RunningVersion,
		ContextID:                  input.ContextID,
		PlacementKubernetesContext: input.PlacementKubernetesContext,
		PlacementServerURL:         input.PlacementServerURL,
		DeleteID:                   input.DeleteID,
	}
}

// EnvDeleteRunner runs one delete attempt to a terminal outcome, persisting
// the environment row accordingly (hard-deleted once the namespace is
// confirmed gone, or moved to deletion-blocked naming why it is not) —
// satisfied by *EnvLifecycle.
type EnvDeleteRunner interface {
	Delete(ctx context.Context, input EnvLifecycleInput) error
}

// EnvDeleter runs the durable env-delete workflow, so a control-plane restart
// resumes an in-flight delete rather than leaving the environment stranded in
// `deleting` until something notices (#1140). It wraps EnvDeleteRunner in a
// DBOS workflow keyed by the delete attempt, mirroring EnvProvisioner's
// wrapping of EnvCoordinator for deploy.
type EnvDeleter struct {
	dbosCtx    dbos.DBOSContext
	runner     EnvDeleteRunner
	workflowFn func(dbos.DBOSContext, EnvDeleteInput) (string, error)
}

// NewEnvDeleter wires the durable deleter.
func NewEnvDeleter(dbosCtx dbos.DBOSContext, runner EnvDeleteRunner) *EnvDeleter {
	d := &EnvDeleter{dbosCtx: dbosCtx, runner: runner}
	// One stable function value shared by RegisterWorkflow and RunWorkflow, which
	// is how DBOS names the workflow and recovers it across restarts.
	d.workflowFn = d.deleteWorkflow
	dbos.RegisterWorkflow(dbosCtx, d.workflowFn)
	return d
}

// Start kicks off one delete attempt asynchronously so the HTTP handler
// returns immediately (bounded, per #1140) while the durable workflow runs
// the actual teardown, however long that takes. input.DeleteID must be a
// fresh id per attempt (see EnvDeleteInput.DeleteID) — the caller's
// responsibility, since both a fresh operator request and a reconciler
// retry need their own.
func (d *EnvDeleter) Start(input EnvDeleteInput) error {
	_, err := dbos.RunWorkflow(d.dbosCtx, d.workflowFn, input, dbos.WithWorkflowID("delete-env-"+input.EnvironmentID+"-"+input.DeleteID))
	return err
}

func (d *EnvDeleter) deleteWorkflow(dctx dbos.DBOSContext, input EnvDeleteInput) (string, error) {
	// One step: the runner is idempotent on re-run (a retried Job create
	// tolerates already-exists and re-watches), so a mid-delete restart
	// resumes cleanly without a second Job.
	return dbos.RunAsStep(dctx, func(c context.Context) (string, error) {
		scoped := security.WithContext(c, security.Context{
			TenantID:   input.TenantID,
			TenantType: input.TenantType,
			ErunUserID: input.ErunUserID,
		})
		if err := d.runner.Delete(scoped, input.lifecycleInput()); err != nil {
			return "deletion-blocked", err
		}
		return "deleted", nil
	})
}

// DefaultDeleteReconcileSchedule is how often EnvDeleteReconciler re-attempts
// every environment mid-teardown: a standard, second-precision cron
// expression, every 5 minutes. Frequent enough that a namespace which
// finishes terminating on its own converges within minutes rather than
// waiting for an operator to notice; infrequent enough that a genuinely wedged
// namespace is not re-attempted so often it never gets to breathe between
// tries.
const DefaultDeleteReconcileSchedule = "0 */5 * * * *"

// DeleteClaimStaleAfter bounds how long a delete may hold its environment's
// claim before a retry (an operator's or the reconciler's) may take it over.
// Longer than the delete Job's own 30-minute deadline (deployexec's
// jobActiveDeadlineSeconds), so a claim only looks stale once the run behind
// it cannot still be live — the same reasoning as routes.deployClaimStaleAfter.
// Shared (not routes-private like deployClaimStaleAfter) because both the
// route handler's initial claim and the reconciler's retry claim need it.
const DeleteClaimStaleAfter = 45 * time.Minute
