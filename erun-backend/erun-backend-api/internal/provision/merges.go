package provision

import (
	"context"
	"log"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/mergeexec"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// MergeCoordinator runs one review's merge-gate attempt to a recorded terminal
// state — the service MergeQueueService.
type MergeCoordinator interface {
	Run(ctx context.Context, reviewID, targetBranch string, params mergeexec.MergeJobParams) error
}

// MergeTenantResolver resolves the caller's tenant name, which names the
// runtime image the merge Job runs in. A small local interface rather than
// routes.ConfigTenantRepository: provision must not depend on routes.
type MergeTenantResolver interface {
	Current(ctx context.Context) (model.Tenant, error)
}

// MergeConfig is the per-instance placement of a merge-gate Job. Unlike
// ReleaseConfig, the workspace claim and repo path are required, not optional:
// a merge Job fetches, commits, and pushes, which needs a real writable
// checkout with push credentials, not whatever the image happens to carry.
type MergeConfig struct {
	Registry       string
	RuntimeVersion string
	Namespace      string
	ServiceAccount string
	HomeClaim      string
	WorkspaceClaim string
	RepoPath       string
}

// Configured reports whether the merge executor has everything it needs to
// launch a Job. Anything missing leaves the queue unwired, so a promotion to
// MERGE is recorded (it already happened, via AdvanceMergeQueue) without
// pretending a gate will run for it.
func (c MergeConfig) Configured() bool {
	return c.Registry != "" && c.RuntimeVersion != "" && c.Namespace != "" && c.ServiceAccount != "" &&
		c.WorkspaceClaim != "" && c.RepoPath != ""
}

// MergeInput is the durable workflow input DBOS checkpoints. BuildID is the
// review's LastReadyBuildID at the moment it was promoted: stable across a
// restart of the same attempt, and distinct for every attempt driven by a new
// build — see AGENTS.md "Merge Queue" for the one path (a manual
// missed-window requeue with no new build) this does not distinguish.
type MergeInput struct {
	TenantID     string `json:"tenantId"`
	TenantType   string `json:"tenantType"`
	ErunUserID   string `json:"erunUserId,omitempty"`
	Tenant       string `json:"tenant"`
	ReviewID     string `json:"reviewId"`
	TargetBranch string `json:"targetBranch"`
	SourceBranch string `json:"sourceBranch"`
	MergeMessage string `json:"mergeMessage"`
	BuildID      string `json:"buildId"`
	Bootstrap    bool   `json:"bootstrap,omitempty"`
}

// MergeQueue runs the durable merge-gate workflow, so a control-plane restart
// resumes an in-flight merge attempt rather than abandoning it or running a
// second one against the same source commit.
type MergeQueue struct {
	dbosCtx      dbos.DBOSContext
	coordinator  MergeCoordinator
	config       MergeConfig
	tenants      MergeTenantResolver
	imageChecker RuntimeImageChecker
	workflowFn   func(dbos.DBOSContext, MergeInput) (string, error)
}

func NewMergeQueue(dbosCtx dbos.DBOSContext, coordinator MergeCoordinator, config MergeConfig, tenants MergeTenantResolver, imageChecker RuntimeImageChecker) *MergeQueue {
	q := &MergeQueue{dbosCtx: dbosCtx, coordinator: coordinator, config: config, tenants: tenants, imageChecker: imageChecker}
	q.workflowFn = q.mergeWorkflow
	dbos.RegisterWorkflow(dbosCtx, q.workflowFn)
	return q
}

// Dispatch starts the merge-gate workflow for a review AdvanceMergeQueue just
// promoted to MERGE. Best-effort: a dispatch failure is logged rather than
// returned, since the caller (BuildService.Create or the manual
// merge-queue/advance route) has already recorded the promotion the review
// itself carries; an operator can still advance the queue again.
func (q *MergeQueue) Dispatch(ctx context.Context, review model.Review) {
	identity, ok := security.FromContext(ctx)
	if !ok {
		log.Printf("erun api merge queue: dispatching review %s: missing security context", review.ReviewID)
		return
	}
	tenant, err := q.tenants.Current(ctx)
	if err != nil {
		log.Printf("erun api merge queue: resolving the tenant to run review %s's merge gate failed: %v", review.ReviewID, err)
		return
	}
	input := MergeInput{
		TenantID:     identity.TenantID,
		TenantType:   identity.TenantType,
		ErunUserID:   identity.ErunUserID,
		Tenant:       tenant.Name,
		ReviewID:     review.ReviewID,
		TargetBranch: review.TargetBranch,
		SourceBranch: review.SourceBranch,
		MergeMessage: review.Name,
		BuildID:      review.LastReadyBuildID,
		Bootstrap:    q.resolveBootstrapImage(tenant.Name),
	}
	if _, err := dbos.RunWorkflow(q.dbosCtx, q.workflowFn, input,
		dbos.WithWorkflowID("merge-"+input.ReviewID+"-"+input.BuildID)); err != nil {
		log.Printf("erun api merge queue: starting the merge gate for review %s failed: %v", review.ReviewID, err)
	}
}

// resolveBootstrapImage mirrors ReleaseQueue.resolveBootstrapImage: the
// synchronous, best-effort precondition run before enqueueing the durable
// workflow, so a resumed workflow does not re-probe the registry.
func (q *MergeQueue) resolveBootstrapImage(tenant string) bool {
	_, bootstrap := ResolveRuntimeImage(context.Background(), q.imageChecker, q.config.Registry, tenant, q.config.RuntimeVersion)
	return bootstrap
}

func (q *MergeQueue) mergeWorkflow(dctx dbos.DBOSContext, input MergeInput) (string, error) {
	params := mergeJobParams(q.config, input)
	identity := security.Context{TenantID: input.TenantID, TenantType: input.TenantType, ErunUserID: input.ErunUserID}
	// One step: the coordinator's own Job create tolerates an already-exists and
	// re-watches the same attempt's Job, so a restart mid-merge resumes watching
	// rather than starting a second attempt.
	return dbos.RunAsStep(dctx, func(c context.Context) (string, error) {
		scoped := security.WithContext(c, identity)
		if err := q.coordinator.Run(scoped, input.ReviewID, input.TargetBranch, params); err != nil {
			return "failed", err
		}
		return "completed", nil
	})
}

// mergeJobParams renders the placement for one merge-gate Job. A tenant with
// no published image (input.Bootstrap) runs the Job on the canonical published
// erun-devops image instead, mirroring the release/deploy Jobs' fallback.
func mergeJobParams(config MergeConfig, input MergeInput) mergeexec.MergeJobParams {
	image := TenantRuntimeImage(config.Registry, input.Tenant, config.RuntimeVersion)
	if input.Bootstrap {
		image = CanonicalRuntimeImage(config.Registry, config.RuntimeVersion)
	}
	return mergeexec.MergeJobParams{
		Tenant:         input.Tenant,
		TargetBranch:   input.TargetBranch,
		SourceBranch:   input.SourceBranch,
		MergeMessage:   input.MergeMessage,
		ReviewID:       input.ReviewID,
		Key:            input.BuildID,
		Namespace:      config.Namespace,
		Image:          image,
		ServiceAccount: config.ServiceAccount,
		HomeClaim:      config.HomeClaim,
		WorkspaceClaim: config.WorkspaceClaim,
		RepoPath:       config.RepoPath,
	}
}
