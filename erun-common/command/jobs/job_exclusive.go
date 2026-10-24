package jobs

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity"
)

func environmentJobExclusiveLeaseID(id string) string {
	return eruncommon.EnvironmentJobExclusiveLeasePrefix + id
}

// heldEnvironmentExclusivityClaim reports the claim currently holding the whole
// environment, if any. Reading through LoadEnvironmentActivityLeases is
// deliberate: that read is also what reclaims an expired or orphaned claim, so
// the answer here can never be a claim nobody holds any more.
func heldEnvironmentExclusivityClaim(tenant, environment string, now time.Time) (activity.EnvironmentActivityLease, bool) {
	claims := eruncommon.HeldEnvironmentExclusivityClaims(tenant, environment, now, environmentExclusivityHoldsWholeEnvironment)
	if len(claims) == 0 {
		return activity.EnvironmentActivityLease{}, false
	}
	return claims[0], true
}

// environmentExclusivityHoldsWholeEnvironment is the one scope that means "no
// other work here at all", rather than "not this resource".
func environmentExclusivityHoldsWholeEnvironment(scope string) bool {
	return strings.TrimSpace(scope) == activity.EnvironmentActivityLeaseScopeEnvironment
}

// startingEnvironmentJobID is the job a start is being made from inside, using
// the same precedence registerEnvironmentJob applies when it records the link:
// an explicit override first (a start forwarded through the MCP edge, whose
// server process inherits no ERUN_JOB_ID however deep the logical nesting), then
// this process's own inherited id.
func startingEnvironmentJobID(params StartEnvironmentJobParams) string {
	if id := strings.TrimSpace(params.StartedByJobID); id != "" {
		return id
	}
	return eruncommon.CurrentEnvironmentJobID()
}

// resolveEnvironmentJobExclusivity decides what this start does about the
// environment's exclusivity, refusing rather than queueing or silently
// proceeding when it cannot. The returned bool is whether *this* job takes the
// claim — false both for ordinary work and for work running under an ancestor's
// claim, which must not take a second one to conflict with the first.
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
	// Reaching here with our own claim id means an earlier run under this same
	// id finished — reserveEnvironmentJobID already refused a still-running one
	// — and left its claim behind. Re-taking it is a renewal, not a conflict.
	if holder.ID == claimID {
		if !params.Exclusive {
			// This start is taking over the id but will never renew the claim,
			// so leaving it would keep refusing everyone else until its TTL
			// elapsed for a job that no longer exists.
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
		if holderJobID, ok := eruncommon.EnvironmentJobIDFromExclusiveLeaseID(holder.ID); ok &&
			eruncommon.EnvironmentJobIsSelfOrDescendantOf(dir, parent, holderJobID) {
			ctx.Trace(fmt.Sprintf("job: %s is held exclusively by job %s, which started this work; running under that claim rather than taking a second one", params.Environment, holderJobID))
			return false, nil
		}
	}
	ctx.Trace(fmt.Sprintf("job: refusing to start %s, %s holds %s exclusively", params.ID, holder.ID, params.Environment))
	return false, eruncommon.EnvironmentExclusivityConflict(fmt.Sprintf("job %q", params.ID), params.Tenant, params.Environment, holder, params.Exclusive, now)
}

// takeEnvironmentJobExclusivityClaim takes the claim at start time rather than
// leaving it to the supervisor, so the refusal is synchronous: a caller learns
// it lost the environment instead of getting a handle to a job that then dies.
// pid is 0 here because the process that will hold this claim does not exist
// yet — the supervisor renews it with its own pid on its first heartbeat, from
// which point liveness reclaim applies to it exactly as it does to any other
// lease. Until then only the TTL bounds it, which is the same bound a remote
// holder has always had.
func takeEnvironmentJobExclusivityClaim(params StartEnvironmentJobParams, now time.Time) (activity.EnvironmentActivityLease, error) {
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

// environmentJobHolder names who a job's own leases belong to, so a refusal
// naming them can say who to go ask instead of naming an unnamed holder. The
// orchestrator id is read from the environment rather than taken as input for
// the same reason the lease store never takes a holder's tenant from caller
// input: a claim must not be able to name someone else as its holder. Shared
// by every lease a job takes on its own behalf — the exclusive claim and the
// plain presence lease alike — so a refusal against either names the same
// initiator.
func environmentJobHolder(tenant string) activity.EnvironmentActivityLeaseHolder {
	return activity.EnvironmentActivityLeaseHolder{
		Orchestrator: strings.TrimSpace(os.Getenv(eruncommon.OrchestratorIDEnvVar)),
		Tenant:       tenant,
	}
}

// releaseEnvironmentJobExclusivityClaim drops a job's claim. Idempotent, and
// scoped to this job's own id, so it can never drop a claim that has since been
// legitimately taken by someone else.
func releaseEnvironmentJobExclusivityClaim(tenant, environment, id string) error {
	_, err := activity.ReleaseExclusiveEnvironmentActivityLease(tenant, environment, activity.EnvironmentActivityLeaseScopeEnvironment, environmentJobExclusiveLeaseID(id))
	return err
}

// environmentJobExclusivityTakeError translates a lost create race into the
// same refusal the pre-flight read produces. Two starts can pass the read
// concurrently; only one create wins, and the loser must be told who won rather
// than shown the store's raw conflict text.
func environmentJobExclusivityTakeError(params StartEnvironmentJobParams, err error, now time.Time) error {
	var conflict *activity.EnvironmentActivityLeaseConflictError
	if !errors.As(err, &conflict) {
		return err
	}
	return eruncommon.EnvironmentExclusivityConflict(fmt.Sprintf("job %q", params.ID), params.Tenant, params.Environment, conflict.Holder, true, now)
}

// DescribeExclusiveJobStartVersionSkew is describeExclusiveClaimVersionSkew
// for job start's off-environment dispatch (exec_raw/exec_agent). A caller on
// a release that added --exclusive can run it against an environment whose
// edge predates the feature entirely: the flag parses fine host-side, but the
// remote edge's own compiled schema is what actually enforces it, so the two
// are not required to agree without this. Without this translation the edge's
// raw schema rejection reads like a malformed call rather than the version
// mismatch it is.
func DescribeExclusiveJobStartVersionSkew(tenant, environment string, exclusive bool, err error) error {
	return activity.DescribeExclusiveClaimVersionSkew(tenant, environment, "job start",
		"upgrade the environment (erun pin / erun deploy) to run this job exclusively there, or drop --exclusive to accept concurrent jobs",
		exclusive, err)
}
