package jobs

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/shared/jobexclusive"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
)

func environmentJobExclusiveLeaseID(id string) string {
	return jobexclusive.EnvironmentJobExclusiveLeasePrefix + id
}

// heldEnvironmentExclusivityClaim reports the claim holding the whole environment.
// The load also reclaims expired or orphaned claims.
func heldEnvironmentExclusivityClaim(tenant, environment string, now time.Time) (model.EnvironmentActivityLease, bool) {
	claims := jobexclusive.HeldEnvironmentExclusivityClaims(tenant, environment, now, environmentExclusivityHoldsWholeEnvironment)
	if len(claims) == 0 {
		return model.EnvironmentActivityLease{}, false
	}
	return claims[0], true
}

func environmentExclusivityHoldsWholeEnvironment(scope string) bool {
	return strings.TrimSpace(scope) == activity.EnvironmentActivityLeaseScopeEnvironment
}

// startingEnvironmentJobID is the parent job of this start: the explicit
// override first (MCP-forwarded starts inherit no ERUN_JOB_ID), then our own.
func startingEnvironmentJobID(params StartEnvironmentJobParams) string {
	if id := strings.TrimSpace(params.StartedByJobID); id != "" {
		return id
	}
	return jobstate.CurrentEnvironmentJobID()
}

// resolveEnvironmentJobExclusivity reports whether this job takes the claim;
// work under an ancestor's claim must not take a second one.
func resolveEnvironmentJobExclusivity(ctx core.Context, dir string, params StartEnvironmentJobParams, now time.Time) (bool, error) {
	claimID := environmentJobExclusiveLeaseID(params.ID)
	holder, held := heldEnvironmentExclusivityClaim(params.Tenant, params.Environment, now)
	if !held {
		if params.Exclusive {
			ctx.Trace(fmt.Sprintf("job: %s is not exclusively held, claiming it for job %s", params.Environment, params.ID))
			return true, nil
		}
		return false, nil
	}
	// Our own id here is a finished earlier run's leftover claim; re-taking it is a renewal.
	if holder.ID == claimID {
		if !params.Exclusive {
			ctx.Trace(fmt.Sprintf("job: dropping job %s's leftover exclusive claim on %s; this start does not claim the environment", params.ID, params.Environment))
			if !ctx.DryRun {
				_ = releaseEnvironmentJobExclusivityClaim(params.Tenant, params.Environment, params.ID)
			}
			return false, nil
		}
		ctx.Trace(fmt.Sprintf("job: reclaiming job %s's own exclusive claim on %s", params.ID, params.Environment))
		return true, nil
	}
	if parent := startingEnvironmentJobID(params); parent != "" {
		if holderJobID, ok := jobexclusive.EnvironmentJobIDFromExclusiveLeaseID(holder.ID); ok &&
			jobexclusive.EnvironmentJobIsSelfOrDescendantOf(dir, parent, holderJobID) {
			ctx.Trace(fmt.Sprintf("job: %s is held exclusively by job %s, which started this work; running under that claim rather than taking a second one", params.Environment, holderJobID))
			return false, nil
		}
	}
	ctx.Trace(fmt.Sprintf("job: refusing to start %s, %s holds %s exclusively", params.ID, holder.ID, params.Environment))
	return false, jobexclusive.EnvironmentExclusivityConflict(fmt.Sprintf("job %q", params.ID), params.Tenant, params.Environment, holder, params.Exclusive, now)
}

// takeEnvironmentJobExclusivityClaim claims at start so refusal is synchronous.
// pid is 0 until the supervisor's first heartbeat; only the TTL bounds it until then.
func takeEnvironmentJobExclusivityClaim(params StartEnvironmentJobParams, now time.Time) (model.EnvironmentActivityLease, error) {
	return activity.TakeEnvironmentActivityLease(activity.TakeEnvironmentActivityLeaseParams{
		Tenant:      params.Tenant,
		Environment: params.Environment,
		Name:        params.Name,
		ID:          environmentJobExclusiveLeaseID(params.ID),
		TTL:         params.LeaseTTL,
		Exclusive:   true,
		Scope:       activity.EnvironmentActivityLeaseScopeEnvironment,
		Holder:      environmentJobHolder(params.Tenant),
		Now:         now,
	})
}

// environmentJobHolder names who a job's leases belong to. The orchestrator id
// comes from the environment so a claim cannot name someone else as holder.
func environmentJobHolder(tenant string) model.EnvironmentActivityLeaseHolder {
	return model.EnvironmentActivityLeaseHolder{
		Orchestrator: strings.TrimSpace(os.Getenv(jobstate.OrchestratorIDEnvVar)),
		Tenant:       tenant,
	}
}

// releaseEnvironmentJobExclusivityClaim drops this job's own claim; idempotent.
func releaseEnvironmentJobExclusivityClaim(tenant, environment, id string) error {
	_, err := activity.ReleaseExclusiveEnvironmentActivityLease(tenant, environment, activity.EnvironmentActivityLeaseScopeEnvironment, environmentJobExclusiveLeaseID(id))
	return err
}

// environmentJobExclusivityTakeError reports a lost create race as the usual refusal.
func environmentJobExclusivityTakeError(params StartEnvironmentJobParams, err error, now time.Time) error {
	var conflict *activity.EnvironmentActivityLeaseConflictError
	if !errors.As(err, &conflict) {
		return err
	}
	return jobexclusive.EnvironmentExclusivityConflict(fmt.Sprintf("job %q", params.ID), params.Tenant, params.Environment, conflict.Holder, true, now)
}

// DescribeExclusiveJobStartVersionSkew explains an edge that predates --exclusive
// rejecting the flag, instead of surfacing a raw schema error.
func DescribeExclusiveJobStartVersionSkew(tenant, environment string, exclusive bool, err error) error {
	return activity.DescribeExclusiveClaimVersionSkew(tenant, environment, "job start",
		"upgrade the environment (erun pin / erun deploy) to run this job exclusively there, or drop --exclusive to accept concurrent jobs",
		exclusive, err)
}
