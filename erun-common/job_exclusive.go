package eruncommon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A plain activity lease is presence: many holders coexist, and taking one says
// nothing about whether anyone else should. That is the right default for
// observability and the wrong one for a gate. Nothing in the job surface could
// express "this work needs the environment to itself", so scheduling a second
// gate batch and a handful of probe jobs into a pod already running one was
// allowed, silently — and cost a 2.4x slowdown, two red runs on tests that pass
// standalone, and a false attribution before a baseline was re-measured alone.
// Worse, two concurrent gate batches sharing one worktree corrupted merge
// accounting: one reported pushing a commit that belonged to the other.
//
// Exclusivity here is one claim on one scope, EnvironmentActivityLeaseScopeEnvironment,
// held for the job's lifetime. Two rules follow from it, and the second is what
// makes it more than a mutex between exclusive jobs:
//
//   - A second job that declares exclusivity is refused.
//   - *Any* job started while an exclusive claim is held is refused too. A gate
//     does not need protection only from another gate; it needs the pod, and a
//     probe job scheduled beside it is exactly what the measurement above
//     recorded.
//
// A refusal names the holder and what to do about it, because a lock whose
// failure mode is "something is wrong somewhere" is one nobody keeps. And the
// claim is a lease, not a lock: it expires without renewal and reconciles
// against the pid recorded on it, so a crashed gate releases the environment on
// the next read rather than pinning it until somebody notices.
//
// The one exemption is lineage. A gate job that starts nested work of its own
// (agent-gate.sh detaching a gate from inside an agent job, an agent's own Bash
// tool) must not be refused by its own ancestor's claim — that is the holder
// starting its own work, not contention — so a start whose parent chain reaches
// the holder proceeds, and deliberately takes no second claim to fight the
// first with.

const (
	// environmentJobExclusiveLeasePrefix keys a job's exclusivity claim
	// separately from the presence lease it also holds, so a lease listing
	// shows the two as distinct claims rather than one id twice, and so the
	// job behind a claim stays recoverable from the claim alone.
	environmentJobExclusiveLeasePrefix = "job-exclusive-"

	// environmentJobAncestorWalkLimit bounds the parent-chain walk the lineage
	// exemption does. Nesting is a handful deep in practice; the bound exists
	// so a corrupted record that points at itself cannot spin.
	environmentJobAncestorWalkLimit = 32
)

func environmentJobExclusiveLeaseID(id string) string {
	return environmentJobExclusiveLeasePrefix + id
}

// environmentJobIDFromExclusiveLeaseID recovers the job an exclusivity claim is
// held for, and reports false for a claim held by anything else — an operator's
// own `erun activity lease take --exclusive --scope environment`, most notably,
// which is a legitimate holder with no job behind it.
func environmentJobIDFromExclusiveLeaseID(leaseID string) (string, bool) {
	id := strings.TrimPrefix(leaseID, environmentJobExclusiveLeasePrefix)
	if id == leaseID || strings.TrimSpace(id) == "" {
		return "", false
	}
	return id, true
}

// EnvironmentExclusivityConflictError is the refusal a start gets when the
// environment is already claimed. It carries the holder rather than only a
// message so a transport can render it structurally, and its own text names the
// holder, the remedy, and the fact that the claim expires — the three things
// missing from a bare "already locked".
type EnvironmentExclusivityConflictError struct {
	// Operation names what was refused, so the refusal reads as a sentence
	// about the caller's own action rather than about the mechanism.
	Operation string
	// Scope is always EnvironmentActivityLeaseScopeEnvironment today; it is
	// carried so a reader never has to assume which scope was contended.
	Scope  string
	Holder EnvironmentActivityLease
	// HolderJobID is the job behind the claim, empty when the holder is not a
	// job (an operator holding the environment directly).
	HolderJobID string
	// Requested says the refused work asked for exclusivity itself, which
	// changes only how the refusal reads: two exclusive claims colliding is a
	// different sentence from ordinary work turned away by one.
	Requested bool
	// Tenant and Environment are echoed so the suggested remedy can be a
	// command the reader can run rather than one they have to complete.
	Tenant      string
	Environment string
	// Remaining is how long the claim had left when this was raised.
	Remaining time.Duration
}

func (e *EnvironmentExclusivityConflictError) Error() string {
	lead := fmt.Sprintf("refusing to run %s: this environment is held exclusively", e.Operation)
	if e.Requested {
		lead = fmt.Sprintf("refusing to run %s exclusively: this environment is already held exclusively", e.Operation)
	}
	message := fmt.Sprintf("%s by %s (%s, lease id %s), and work that claims the environment must not overlap with anything else here",
		lead, e.Holder.Holder.String(), e.Holder.Name, e.Holder.ID)
	message += fmt.Sprintf("\nthe claim expires in %s unless its holder renews it, and is reclaimed on the next read if the holder's process is gone or its pod was replaced",
		formatEnvironmentExclusivityRemaining(e.Remaining))
	if e.HolderJobID != "" && e.Tenant != "" && e.Environment != "" {
		message += fmt.Sprintf("\nwait for it, or cancel it: erun exec job cancel --tenant %s --environment %s --id %s",
			e.Tenant, e.Environment, e.HolderJobID)
		return message
	}
	if e.Tenant != "" && e.Environment != "" {
		message += fmt.Sprintf("\nwait for it, or release it: erun activity lease release --tenant %s --environment %s --id %s --exclusive --scope %s",
			e.Tenant, e.Environment, e.Holder.ID, e.Scope)
	}
	return message
}

func formatEnvironmentExclusivityRemaining(remaining time.Duration) string {
	if remaining <= 0 {
		return "0s"
	}
	return remaining.Round(time.Second).String()
}

// heldEnvironmentExclusivityClaim reports the claim currently holding the whole
// environment, if any. Reading through LoadEnvironmentActivityLeases is
// deliberate: that read is also what reclaims an expired or orphaned claim, so
// the answer here can never be a claim nobody holds any more.
func heldEnvironmentExclusivityClaim(tenant, environment string, now time.Time) (EnvironmentActivityLease, bool) {
	held, err := LoadEnvironmentActivityLeases(tenant, environment, now)
	if err != nil {
		return EnvironmentActivityLease{}, false
	}
	for _, lease := range held {
		if lease.Exclusive && lease.Scope == EnvironmentActivityLeaseScopeEnvironment {
			return lease, true
		}
	}
	return EnvironmentActivityLease{}, false
}

// environmentExclusivityConflict builds the refusal for a claim this caller is
// not entitled to work under.
func environmentExclusivityConflict(operation, tenant, environment string, holder EnvironmentActivityLease, requested bool, now time.Time) error {
	holderJobID, _ := environmentJobIDFromExclusiveLeaseID(holder.ID)
	return &EnvironmentExclusivityConflictError{
		Operation:   operation,
		Scope:       EnvironmentActivityLeaseScopeEnvironment,
		Holder:      holder,
		HolderJobID: holderJobID,
		Requested:   requested,
		Tenant:      tenant,
		Environment: environment,
		Remaining:   holder.ExpiresAt.Sub(now),
	}
}

// EnsureEnvironmentNotExclusivelyHeld refuses when something else holds the
// whole environment. It is for work that mutates state a claim exists to
// protect but is not itself a job — `exec gate-merge`, which rewrites the shared
// worktree onto a target branch, is the case it was added for: two merge-queue
// drives racing that one worktree is how a batch came to report pushing another
// batch's commit.
//
// underLeaseID is how a caller that took the claim itself says so. A
// multi-step drive cannot express its hold as a job — its steps are separate
// processes with no common supervisor — so it takes the claim directly and
// names it here. Without this the mechanism would refuse the very caller it
// was protecting, which is a dead end rather than a safeguard.
//
// Off-pod this is a no-op: an exclusivity claim is environment state, and a
// command run on an operator's own laptop is not competing for a pod's cores.
func EnsureEnvironmentNotExclusivelyHeld(ctx Context, what, underLeaseID string) error {
	tenant, environment, ok := injectedRuntimePodIdentity(os.Getenv)
	if !ok {
		return nil
	}
	now := time.Now()
	holder, held := heldEnvironmentExclusivityClaim(tenant, environment, now)
	if !held {
		ctx.Trace(fmt.Sprintf("%s: no exclusive claim on %s/%s, proceeding", what, tenant, environment))
		return nil
	}
	if environmentExclusivityClaimedByCaller(holder, underLeaseID) {
		ctx.Trace(fmt.Sprintf("%s: running under the caller's own exclusive claim %s, proceeding", what, holder.ID))
		return nil
	}
	// A job's own nested work is entitled to run under the claim its own
	// ancestor took, exactly as a nested job start is.
	if environmentExclusivityHeldByOwnLineage(tenant, environment, holder) {
		ctx.Trace(fmt.Sprintf("%s: running under this caller's own job's exclusive claim %s, proceeding", what, holder.ID))
		return nil
	}
	ctx.Trace(fmt.Sprintf("%s: refused, %s holds %s/%s exclusively", what, holder.ID, tenant, environment))
	return environmentExclusivityConflict(what, tenant, environment, holder, false, now)
}

// environmentExclusivityClaimedByCaller compares a caller's declared claim id
// against the held one, through the store's own id normalisation on both sides.
// Comparing raw against sanitized is how erun#1652 happened: an id needing
// sanitisation never matched its stored form, so every renewal misread as a
// fresh claim — here the same mistake would refuse a caller its own claim.
func environmentExclusivityClaimedByCaller(holder EnvironmentActivityLease, underLeaseID string) bool {
	if strings.TrimSpace(underLeaseID) == "" {
		return false
	}
	resolved, err := ResolveEnvironmentActivityLeaseID(underLeaseID, underLeaseID)
	if err != nil {
		return false
	}
	return holder.ID == resolved
}

// environmentExclusivityHeldByOwnLineage reports whether the claim belongs to a
// job this process is running inside — its own job, or one that started it,
// transitively.
func environmentExclusivityHeldByOwnLineage(tenant, environment string, holder EnvironmentActivityLease) bool {
	holderJobID, ok := environmentJobIDFromExclusiveLeaseID(holder.ID)
	if !ok {
		return false
	}
	current := CurrentEnvironmentJobID()
	if current == "" {
		return false
	}
	dir, err := environmentJobDir(tenant, environment)
	if err != nil {
		return false
	}
	return environmentJobIsSelfOrDescendantOf(dir, current, holderJobID)
}

// environmentJobIsSelfOrDescendantOf walks jobID's parent chain looking for
// ancestorID. It reads StartedByJobID, the same link a job's own finish check
// uses to tell work it started from an unrelated job sharing the environment,
// so the two agree on what "mine" means without either tracking it separately.
func environmentJobIsSelfOrDescendantOf(dir, jobID, ancestorID string) bool {
	jobID = strings.TrimSpace(jobID)
	ancestorID = strings.TrimSpace(ancestorID)
	if jobID == "" || ancestorID == "" {
		return false
	}
	seen := make(map[string]struct{}, environmentJobAncestorWalkLimit)
	for hop := 0; hop < environmentJobAncestorWalkLimit; hop++ {
		if jobID == ancestorID {
			return true
		}
		if _, repeated := seen[jobID]; repeated {
			return false
		}
		seen[jobID] = struct{}{}
		job, err := readEnvironmentJob(filepath.Join(dir, jobID+".json"))
		if err != nil {
			return false
		}
		jobID = strings.TrimSpace(job.StartedByJobID)
		if jobID == "" {
			return false
		}
	}
	return false
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
	return CurrentEnvironmentJobID()
}

// resolveEnvironmentJobExclusivity decides what this start does about the
// environment's exclusivity, refusing rather than queueing or silently
// proceeding when it cannot. The returned bool is whether *this* job takes the
// claim — false both for ordinary work and for work running under an ancestor's
// claim, which must not take a second one to conflict with the first.
func resolveEnvironmentJobExclusivity(ctx Context, dir string, params StartEnvironmentJobParams, now time.Time) (bool, error) {
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
		if holderJobID, ok := environmentJobIDFromExclusiveLeaseID(holder.ID); ok &&
			environmentJobIsSelfOrDescendantOf(dir, parent, holderJobID) {
			ctx.Trace(fmt.Sprintf("job: %s is held exclusively by job %s, which started this work; running under that claim rather than taking a second one", params.Environment, holderJobID))
			return false, nil
		}
	}
	ctx.Trace(fmt.Sprintf("job: refusing to start %s, %s holds %s exclusively", params.ID, holder.ID, params.Environment))
	return false, environmentExclusivityConflict(fmt.Sprintf("job %q", params.ID), params.Tenant, params.Environment, holder, params.Exclusive, now)
}

// takeEnvironmentJobExclusivityClaim takes the claim at start time rather than
// leaving it to the supervisor, so the refusal is synchronous: a caller learns
// it lost the environment instead of getting a handle to a job that then dies.
// pid is 0 here because the process that will hold this claim does not exist
// yet — the supervisor renews it with its own pid on its first heartbeat, from
// which point liveness reclaim applies to it exactly as it does to any other
// lease. Until then only the TTL bounds it, which is the same bound a remote
// holder has always had.
func takeEnvironmentJobExclusivityClaim(params StartEnvironmentJobParams, now time.Time) (EnvironmentActivityLease, error) {
	return TakeEnvironmentActivityLease(TakeEnvironmentActivityLeaseParams{
		Tenant:      params.Tenant,
		Environment: params.Environment,
		Name:        params.Name,
		ID:          environmentJobExclusiveLeaseID(params.ID),
		TTL:         params.LeaseTTL,
		Exclusive:   true,
		Scope:       EnvironmentActivityLeaseScopeEnvironment,
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
func environmentJobHolder(tenant string) EnvironmentActivityLeaseHolder {
	return EnvironmentActivityLeaseHolder{
		Orchestrator: strings.TrimSpace(os.Getenv("ERUN_ORCHESTRATOR_ID")),
		Tenant:       tenant,
	}
}

// releaseEnvironmentJobExclusivityClaim drops a job's claim. Idempotent, and
// scoped to this job's own id, so it can never drop a claim that has since been
// legitimately taken by someone else.
func releaseEnvironmentJobExclusivityClaim(tenant, environment, id string) error {
	return ReleaseExclusiveEnvironmentActivityLease(tenant, environment, EnvironmentActivityLeaseScopeEnvironment, environmentJobExclusiveLeaseID(id))
}

// environmentJobExclusivityTakeError translates a lost create race into the
// same refusal the pre-flight read produces. Two starts can pass the read
// concurrently; only one create wins, and the loser must be told who won rather
// than shown the store's raw conflict text.
func environmentJobExclusivityTakeError(params StartEnvironmentJobParams, err error, now time.Time) error {
	var conflict *EnvironmentActivityLeaseConflictError
	if !errors.As(err, &conflict) {
		return err
	}
	return environmentExclusivityConflict(fmt.Sprintf("job %q", params.ID), params.Tenant, params.Environment, conflict.Holder, true, now)
}
