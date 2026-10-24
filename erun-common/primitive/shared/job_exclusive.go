package shared

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity"
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
// (an agent detaching a gate from its own Bash tool inside an agent job) must not be refused by its own ancestor's claim — that is the holder
// starting its own work, not contention — so a start whose parent chain reaches
// the holder proceeds, and deliberately takes no second claim to fight the
// first with.

const (
	// EnvironmentJobExclusiveLeasePrefix keys a job's exclusivity claim
	// separately from the presence lease it also holds, so a lease listing
	// shows the two as distinct claims rather than one id twice, and so the
	// job behind a claim stays recoverable from the claim alone.
	EnvironmentJobExclusiveLeasePrefix = "job-exclusive-"

	// environmentJobAncestorWalkLimit bounds the parent-chain walk the lineage
	// exemption does. Nesting is a handful deep in practice; the bound exists
	// so a corrupted record that points at itself cannot spin.
	environmentJobAncestorWalkLimit = 32
)

// EnvironmentJobIDFromExclusiveLeaseID recovers the job an exclusivity claim is
// held for, and reports false for a claim held by anything else — an operator's
// own `erun activity lease take --exclusive --scope environment`, most notably,
// which is a legitimate holder with no job behind it.
func EnvironmentJobIDFromExclusiveLeaseID(leaseID string) (string, bool) {
	id := strings.TrimPrefix(leaseID, EnvironmentJobExclusiveLeasePrefix)
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
	// Scope is the scope the holder actually took its claim on, carried so a
	// reader never has to assume which scope was contended — and so the
	// release remedy below names the store the claim is really in, rather than
	// the environment scope an exclusive take only uses when asked for it.
	Scope  string
	Holder activity.EnvironmentActivityLease
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

// environmentExclusivityCoversWorktree reports whether a claim in scope covers
// the environment's one shared worktree, which is what `exec gate-merge`
// rewrites. The default scope claims exactly that resource, and the
// environment scope claims everything here, so it covers the worktree too. Any
// other scope names a resource of the caller's own choosing — a second clone's
// checkout, the case scopes exist for -- and a claim on it must not refuse work
// in a different one.
func environmentExclusivityCoversWorktree(scope string) bool {
	switch strings.TrimSpace(scope) {
	case activity.DefaultEnvironmentActivityLeaseScope, activity.EnvironmentActivityLeaseScopeEnvironment:
		return true
	default:
		return false
	}
}

// HeldEnvironmentExclusivityClaims returns every held exclusive claim whose
// scope satisfies covers, in the store's own deterministic order. A slice
// rather than the one claim, because two scopes can be held at once, one claim
// per scope, and a caller entitled to work under one of them is not thereby
// entitled to work under the other.
func HeldEnvironmentExclusivityClaims(tenant, environment string, now time.Time, covers func(string) bool) []activity.EnvironmentActivityLease {
	held, err := activity.LoadEnvironmentActivityLeases(tenant, environment, now)
	if err != nil {
		return nil
	}
	var claims []activity.EnvironmentActivityLease
	for _, lease := range held {
		if lease.Exclusive && covers(lease.Scope) {
			claims = append(claims, lease)
		}
	}
	return claims
}

// environmentExclusivityExemption names why this caller may run under a held
// claim, or returns "" when it may not: either it took the claim itself and
// named it, or the claim belongs to a job it is running inside.
func environmentExclusivityExemption(tenant, environment string, holder activity.EnvironmentActivityLease, underLeaseID string) string {
	if environmentExclusivityClaimedByCaller(holder, underLeaseID) {
		return fmt.Sprintf("running under the caller's own exclusive claim %s", holder.ID)
	}
	if environmentExclusivityHeldByOwnLineage(tenant, environment, holder) {
		return fmt.Sprintf("running under this caller's own job's exclusive claim %s", holder.ID)
	}
	return ""
}

// EnvironmentExclusivityConflict builds the refusal for a claim this caller is
// not entitled to work under.
func EnvironmentExclusivityConflict(operation, tenant, environment string, holder activity.EnvironmentActivityLease, requested bool, now time.Time) error {
	holderJobID, _ := EnvironmentJobIDFromExclusiveLeaseID(holder.ID)
	return &EnvironmentExclusivityConflictError{
		Operation:   operation,
		Scope:       activity.NormalizeExclusiveEnvironmentActivityLeaseScope(holder.Scope),
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
func EnsureEnvironmentNotExclusivelyHeld(ctx core.Context, what, underLeaseID string) error {
	tenant, environment, ok := core.InjectedRuntimePodIdentity(os.Getenv)
	if !ok {
		return nil
	}
	now := time.Now()
	// Every exclusive claim covering the worktree this rewrites, not only the
	// one claiming the whole environment. An exclusive take defaults to the
	// worktree scope, so reading the environment scope alone found nothing to
	// refuse and let the drive rewrite the tree under a claim that was held —
	// failing open for exactly the caller, an orchestrator holding the worktree
	// for its whole window, this guard was written for.
	claims := HeldEnvironmentExclusivityClaims(tenant, environment, now, environmentExclusivityCoversWorktree)
	if len(claims) == 0 {
		ctx.Trace(fmt.Sprintf("%s: no exclusive claim on %s/%s, proceeding", what, tenant, environment))
		return nil
	}
	// A caller entitled to work under one claim is not thereby entitled to work
	// under another, so every covering claim is accounted for before this
	// proceeds: the first one the caller is not exempt from refuses the whole
	// operation, and nothing is traced as proceeding until none is left.
	exemptions := make([]string, 0, len(claims))
	for _, claim := range claims {
		exemption := environmentExclusivityExemption(tenant, environment, claim, underLeaseID)
		if exemption == "" {
			ctx.Trace(fmt.Sprintf("%s: refused, %s holds %s/%s exclusively", what, claim.ID, tenant, environment))
			return EnvironmentExclusivityConflict(what, tenant, environment, claim, false, now)
		}
		exemptions = append(exemptions, fmt.Sprintf("%s: %s, proceeding", what, exemption))
	}
	for _, exemption := range exemptions {
		ctx.Trace(exemption)
	}
	return nil
}

// environmentExclusivityClaimedByCaller compares a caller's declared claim id
// against the held one, through the store's own id normalisation on both sides.
// Comparing raw against sanitized is how erun#1652 happened: an id needing
// sanitisation never matched its stored form, so every renewal misread as a
// fresh claim — here the same mistake would refuse a caller its own claim.
func environmentExclusivityClaimedByCaller(holder activity.EnvironmentActivityLease, underLeaseID string) bool {
	if strings.TrimSpace(underLeaseID) == "" {
		return false
	}
	resolved, err := activity.ResolveEnvironmentActivityLeaseID(underLeaseID, underLeaseID)
	if err != nil {
		return false
	}
	return holder.ID == resolved
}

// environmentExclusivityHeldByOwnLineage reports whether the claim belongs to a
// job this process is running inside — its own job, or one that started it,
// transitively.
func environmentExclusivityHeldByOwnLineage(tenant, environment string, holder activity.EnvironmentActivityLease) bool {
	holderJobID, ok := EnvironmentJobIDFromExclusiveLeaseID(holder.ID)
	if !ok {
		return false
	}
	current := CurrentEnvironmentJobID()
	if current == "" {
		return false
	}
	dir, err := EnvironmentJobDir(tenant, environment)
	if err != nil {
		return false
	}
	return EnvironmentJobIsSelfOrDescendantOf(dir, current, holderJobID)
}

// EnvironmentJobIsSelfOrDescendantOf walks jobID's parent chain looking for
// ancestorID. It reads StartedByJobID, the same link a job's own finish check
// uses to tell work it started from an unrelated job sharing the environment,
// so the two agree on what "mine" means without either tracking it separately.
func EnvironmentJobIsSelfOrDescendantOf(dir, jobID, ancestorID string) bool {
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
		job, err := ReadEnvironmentJob(filepath.Join(dir, jobID+".json"))
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
