package provision

import (
	"context"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// EnvDeleteInput is the delete workflow's checkpointed, non-secret input.
type EnvDeleteInput struct {
	TenantID      string `json:"tenantId"`
	TenantType    string `json:"tenantType"`
	ErunUserID    string `json:"erunUserId,omitempty"`
	EnvironmentID string `json:"environmentId"`
	Tenant        string `json:"tenant"`
	Environment   string `json:"environment"`
	// RunningVersion is empty when the environment never deployed, so there is no namespace.
	RunningVersion             string `json:"runningVersion,omitempty"`
	ContextID                  string `json:"contextId,omitempty"`
	PlacementKubernetesContext string `json:"placementKubernetesContext,omitempty"`
	PlacementServerURL         string `json:"placementServerUrl,omitempty"`
	// DeleteID keys the workflow per attempt: an environment-keyed id would replay
	// a completed attempt's cached result instead of running again.
	DeleteID string `json:"deleteId"`
}

func (input EnvDeleteInput) lifecycleInput() EnvLifecycleInput {
	return EnvLifecycleInput{
		TenantID:                   input.TenantID,
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

// EnvDeleteRunner runs one delete attempt to a terminal outcome.
type EnvDeleteRunner interface {
	Delete(ctx context.Context, input EnvLifecycleInput) error
}

// EnvDeleter runs the durable delete workflow so a restart resumes an in-flight delete.
type EnvDeleter struct {
	dbosCtx    dbos.DBOSContext
	runner     EnvDeleteRunner
	workflowFn func(dbos.DBOSContext, EnvDeleteInput) (string, error)
}

func NewEnvDeleter(dbosCtx dbos.DBOSContext, runner EnvDeleteRunner) *EnvDeleter {
	d := &EnvDeleter{dbosCtx: dbosCtx, runner: runner}
	// DBOS names and recovers the workflow by this one stable function value.
	d.workflowFn = d.deleteWorkflow
	dbos.RegisterWorkflow(dbosCtx, d.workflowFn)
	return d
}

// Start runs one delete attempt asynchronously. input.DeleteID must be fresh per attempt.
func (d *EnvDeleter) Start(input EnvDeleteInput) error {
	_, err := dbos.RunWorkflow(d.dbosCtx, d.workflowFn, input, dbos.WithWorkflowID("delete-env-"+input.EnvironmentID+"-"+input.DeleteID))
	return err
}

func (d *EnvDeleter) deleteWorkflow(dctx dbos.DBOSContext, input EnvDeleteInput) (string, error) {
	// The runner is idempotent on re-run, so a restart resumes without a second Job.
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

// DefaultDeleteReconcileSchedule re-attempts environments mid-teardown every 5 minutes.
const DefaultDeleteReconcileSchedule = "0 */5 * * * *"

// DeleteClaimStaleAfter exceeds the delete Job's 30-minute deadline, so a claim
// only looks stale once its run cannot still be live.
const DeleteClaimStaleAfter = 45 * time.Minute
