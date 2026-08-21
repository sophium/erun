package provision

import (
	"context"
	"fmt"
	"log"
	"strconv"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/releaseexec"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

// ReleaseInput is the durable workflow input DBOS checkpoints: tenant identity
// plus the release attempt's own coordinates. No secret is carried — the release
// Job runs under its own scoped ServiceAccount in the tenant's namespace.
type ReleaseInput struct {
	TenantID     string `json:"tenantId"`
	TenantType   string `json:"tenantType"`
	ErunUserID   string `json:"erunUserId,omitempty"`
	ReleaseID    string `json:"releaseId"`
	Attempt      int    `json:"attempt"`
	Tenant       string `json:"tenant"`
	TargetBranch string `json:"targetBranch"`
	CommitID     string `json:"commitId"`
	// Bootstrap is decided once, synchronously, before the durable workflow is
	// enqueued (resolveBootstrapImage), and checkpointed here so a resumed
	// workflow does not re-probe the registry — the same pattern
	// EnvProvisionInput.Bootstrap uses for the deploy Job. True means the
	// tenant's own <tenant>-devops image was confirmed missing at dispatch
	// time, so the release Job runs the canonical published erun-devops image
	// instead of one that can only ImagePullBackOff on an image nobody ever
	// pushed.
	Bootstrap bool `json:"bootstrap,omitempty"`
}

// ReleaseCoordinator runs one claimed release attempt to a recorded terminal
// state — the service ReleaseService.
type ReleaseCoordinator interface {
	Run(ctx context.Context, release model.Release, params releaseexec.ReleaseJobParams) error
	Get(ctx context.Context, releaseID string) (model.Release, error)
	Dispatch(ctx context.Context, start func(model.Release) error) (int, error)
	DispatchAfterCooldown(ctx context.Context, start func(model.Release) error) (int, error)
}

// ReleaseConfig is the per-instance placement of a release Job: the environment
// whose warm fingerprint cache and BuildKit state the release should run beside,
// the image it runs, and the ServiceAccount it runs as.
type ReleaseConfig struct {
	// Registry the tenant's runtime image is pulled from.
	Registry string
	// RuntimeVersion is the tag of the runtime image the release runs in — the
	// currently-installed version, not the one being released.
	RuntimeVersion string
	// Namespace is the agent environment's namespace, so the Job lands beside that
	// environment's volumes.
	Namespace string
	// ServiceAccount the release Job runs as.
	ServiceAccount string
	// HomeClaim, WorkspaceClaim and RepoPath name the agent environment's own
	// volumes and checkout. Empty leaves the release on the image-baked project
	// root, which is enough to exercise the executor but has no warm cache.
	HomeClaim      string
	WorkspaceClaim string
	RepoPath       string
	// DryRun runs `erun release --dry-run`, which resolves the release without
	// publishing anything or moving a public ref. It is how the executor is
	// exercised against a scoped target rather than cutting a real version.
	DryRun bool
}

// Configured reports whether the release executor has everything it needs to
// launch a Job. Anything missing leaves the queue unwired, so a trigger records
// the request without pretending it will run.
func (c ReleaseConfig) Configured() bool {
	return c.Registry != "" && c.RuntimeVersion != "" && c.Namespace != "" && c.ServiceAccount != ""
}

// ReleaseQueue runs the durable release workflow, so a control-plane restart
// resumes an in-flight release rather than abandoning it or starting a second
// one. The workflow is keyed by the release attempt: an id keyed by the release
// alone is terminal after the first run, which would make a retry a silent
// replay of the previous attempt's outcome instead of a new run.
type ReleaseQueue struct {
	dbosCtx      dbos.DBOSContext
	coordinator  ReleaseCoordinator
	config       ReleaseConfig
	imageChecker RuntimeImageChecker
	workflowFn   func(dbos.DBOSContext, ReleaseInput) (string, error)
}

// NewReleaseQueue wires the durable release workflow. imageChecker may be
// nil, which skips the published-image fallback and always names the
// tenant's own image.
func NewReleaseQueue(dbosCtx dbos.DBOSContext, coordinator ReleaseCoordinator, config ReleaseConfig, imageChecker RuntimeImageChecker) *ReleaseQueue {
	q := &ReleaseQueue{dbosCtx: dbosCtx, coordinator: coordinator, config: config, imageChecker: imageChecker}
	// One stable function value shared by RegisterWorkflow and RunWorkflow, which
	// is how DBOS names the workflow and recovers it across restarts.
	q.workflowFn = q.releaseWorkflow
	dbos.RegisterWorkflow(dbosCtx, q.workflowFn)
	return q
}

// Dispatch drains what the queue may start right now, launching each claimed
// release as its own durable workflow. The tenant name is the caller's to
// supply: it names the runtime image the release runs in, and the row carries a
// tenant id, not a name. The request-scoped identity is carried into the
// workflow input so the workflow's steps rebind to the right tenant after a
// restart.
func (q *ReleaseQueue) Dispatch(ctx context.Context, tenantName string) (int, error) {
	identity, ok := security.FromContext(ctx)
	if !ok {
		return 0, fmt.Errorf("missing security context")
	}
	return q.coordinator.Dispatch(ctx, func(release model.Release) error {
		return q.start(identity, tenantName, release)
	})
}

func (q *ReleaseQueue) start(identity security.Context, tenantName string, release model.Release) error {
	input := ReleaseInput{
		TenantID:     identity.TenantID,
		TenantType:   identity.TenantType,
		ErunUserID:   identity.ErunUserID,
		ReleaseID:    release.ReleaseID,
		Attempt:      release.Attempt,
		Tenant:       tenantName,
		TargetBranch: release.TargetBranch,
		CommitID:     release.CommitID,
		Bootstrap:    q.resolveBootstrapImage(tenantName),
	}
	_, err := dbos.RunWorkflow(q.dbosCtx, q.workflowFn, input,
		dbos.WithWorkflowID("release-"+release.ReleaseID+"-"+strconv.Itoa(release.Attempt)))
	return err
}

// resolveBootstrapImage mirrors EnvProvisioner.resolveBootstrapImage: the
// synchronous, best-effort precondition run before enqueueing the durable
// workflow, so a resumed workflow does not re-probe the registry.
func (q *ReleaseQueue) resolveBootstrapImage(tenant string) bool {
	_, bootstrap := ResolveRuntimeImage(context.Background(), q.imageChecker, q.config.Registry, tenant, q.config.RuntimeVersion)
	return bootstrap
}

func (q *ReleaseQueue) releaseWorkflow(dctx dbos.DBOSContext, input ReleaseInput) (string, error) {
	params := releaseJobParams(q.config, input)
	// One step: the coordinator is idempotent on re-run (the Job create tolerates
	// an already-exists, then re-watches the same attempt's Job), so a restart
	// mid-release resumes watching rather than starting a second release.
	identity := security.Context{
		TenantID:   input.TenantID,
		TenantType: input.TenantType,
		ErunUserID: input.ErunUserID,
	}
	outcome, runErr := dbos.RunAsStep(dctx, func(c context.Context) (string, error) {
		scoped := security.WithContext(c, identity)
		release, err := q.coordinator.Get(scoped, input.ReleaseID)
		if err != nil {
			return "failed", err
		}
		if err := q.coordinator.Run(scoped, release, params); err != nil {
			return "failed", err
		}
		return "released", nil
	})
	// Handing the slot on is its own step, so a control plane that restarts here
	// resumes at the dispatch rather than re-running a release that already
	// finished. It happens after a failure too: a failed release must not block
	// the queue behind it.
	_, _ = dbos.RunAsStep(dctx, func(c context.Context) (string, error) {
		q.dispatchNext(security.WithContext(c, identity), identity, input.Tenant)
		return "dispatched", nil
	})
	return outcome, runErr
}

// dispatchNext hands the freed slot to whatever is queued behind this release,
// once the cooldown this release's own terminal write opened has passed. A
// failure here is not this release's failure — it already reached a terminal
// state — so it is logged and the queue picks the work up on the next trigger.
func (q *ReleaseQueue) dispatchNext(ctx context.Context, identity security.Context, tenantName string) {
	if _, err := q.coordinator.DispatchAfterCooldown(ctx, func(next model.Release) error {
		return q.start(identity, tenantName, next)
	}); err != nil {
		log.Printf("erun api release queue: starting the next queued release for tenant %q did not happen: %v", tenantName, err)
	}
}

// releaseJobParams renders the placement for one release Job. A tenant with
// no published image (input.Bootstrap, decided once by resolveBootstrapImage
// before the durable workflow runs) instead runs the Job on the canonical
// published erun-devops image, the same fallback the deploy/stop/delete Jobs
// apply.
func releaseJobParams(config ReleaseConfig, input ReleaseInput) releaseexec.ReleaseJobParams {
	image := TenantRuntimeImage(config.Registry, input.Tenant, config.RuntimeVersion)
	if input.Bootstrap {
		image = CanonicalRuntimeImage(config.Registry, config.RuntimeVersion)
	}
	return releaseexec.ReleaseJobParams{
		Tenant:         input.Tenant,
		TargetBranch:   input.TargetBranch,
		CommitID:       input.CommitID,
		ReleaseID:      input.ReleaseID,
		Attempt:        input.Attempt,
		Namespace:      config.Namespace,
		Image:          image,
		ServiceAccount: config.ServiceAccount,
		HomeClaim:      config.HomeClaim,
		WorkspaceClaim: config.WorkspaceClaim,
		RepoPath:       config.RepoPath,
		DryRun:         config.DryRun,
	}
}
