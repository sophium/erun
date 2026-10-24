package shared

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
)

// An exclusive claim (EnvironmentActivityLeaseScopeEnvironment) refuses any other
// job start while held, not only other exclusive ones. It is a lease that expires
// and reconciles against its pid. A job started from within the holder's own job
// lineage is exempt and takes no second claim.

const (
	// EnvironmentJobExclusiveLeasePrefix keys a job's exclusivity claim apart from its presence lease.
	EnvironmentJobExclusiveLeasePrefix = "job-exclusive-"

	// environmentJobAncestorWalkLimit stops a self-referencing record from spinning.
	environmentJobAncestorWalkLimit = 32
)

// EnvironmentJobIDFromExclusiveLeaseID returns the job behind a claim; false for a
// non-job holder such as an operator's own exclusive take.
func EnvironmentJobIDFromExclusiveLeaseID(leaseID string) (string, bool) {
	id := strings.TrimPrefix(leaseID, EnvironmentJobExclusiveLeasePrefix)
	if id == leaseID || strings.TrimSpace(id) == "" {
		return "", false
	}
	return id, true
}

// EnvironmentExclusivityConflictError is the refusal a start gets when the environment is claimed.
type EnvironmentExclusivityConflictError struct {
	Operation string
	// Scope is the holder's actual scope, so the release remedy names the right store.
	Scope  string
	Holder model.EnvironmentActivityLease
	// HolderJobID is empty when the holder is not a job.
	HolderJobID string
	// Requested says the refused work itself asked for exclusivity.
	Requested   bool
	Tenant      string
	Environment string
	Remaining   time.Duration
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

// environmentExclusivityCoversWorktree: the default and environment scopes cover the
// shared worktree; any other scope names a separate resource.
func environmentExclusivityCoversWorktree(scope string) bool {
	switch strings.TrimSpace(scope) {
	case activity.DefaultEnvironmentActivityLeaseScope, activity.EnvironmentActivityLeaseScopeEnvironment:
		return true
	default:
		return false
	}
}

// HeldEnvironmentExclusivityClaims returns every held exclusive claim whose scope satisfies covers.
func HeldEnvironmentExclusivityClaims(tenant, environment string, now time.Time, covers func(string) bool) []model.EnvironmentActivityLease {
	held, err := activity.LoadEnvironmentActivityLeases(tenant, environment, now)
	if err != nil {
		return nil
	}
	var claims []model.EnvironmentActivityLease
	for _, lease := range held {
		if lease.Exclusive && covers(lease.Scope) {
			claims = append(claims, lease)
		}
	}
	return claims
}

// environmentExclusivityExemption returns why the caller may run under holder, or "".
func environmentExclusivityExemption(tenant, environment string, holder model.EnvironmentActivityLease, underLeaseID string) string {
	if environmentExclusivityClaimedByCaller(holder, underLeaseID) {
		return fmt.Sprintf("running under the caller's own exclusive claim %s", holder.ID)
	}
	if environmentExclusivityHeldByOwnLineage(tenant, environment, holder) {
		return fmt.Sprintf("running under this caller's own job's exclusive claim %s", holder.ID)
	}
	return ""
}

// EnvironmentExclusivityConflict builds the refusal for a claim the caller is not entitled to.
func EnvironmentExclusivityConflict(operation, tenant, environment string, holder model.EnvironmentActivityLease, requested bool, now time.Time) error {
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

// EnsureEnvironmentNotExclusivelyHeld refuses non-job mutating work (e.g. exec gate-merge)
// while another holder claims it. underLeaseID names a claim the caller took itself.
// No-op off-pod.
func EnsureEnvironmentNotExclusivelyHeld(ctx core.Context, what, underLeaseID string) error {
	tenant, environment, ok := core.InjectedRuntimePodIdentity(os.Getenv)
	if !ok {
		return nil
	}
	now := time.Now()
	// An exclusive take defaults to the worktree scope, so the environment scope alone is not enough.
	claims := HeldEnvironmentExclusivityClaims(tenant, environment, now, environmentExclusivityCoversWorktree)
	if len(claims) == 0 {
		ctx.Trace(fmt.Sprintf("%s: no exclusive claim on %s/%s, proceeding", what, tenant, environment))
		return nil
	}
	// Every covering claim must be exempt before proceeding.
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

// environmentExclusivityClaimedByCaller normalises both ids, so a sanitised id still matches.
func environmentExclusivityClaimedByCaller(holder model.EnvironmentActivityLease, underLeaseID string) bool {
	if strings.TrimSpace(underLeaseID) == "" {
		return false
	}
	resolved, err := activity.ResolveEnvironmentActivityLeaseID(underLeaseID, underLeaseID)
	if err != nil {
		return false
	}
	return holder.ID == resolved
}

// environmentExclusivityHeldByOwnLineage reports whether the claim belongs to this job or an ancestor.
func environmentExclusivityHeldByOwnLineage(tenant, environment string, holder model.EnvironmentActivityLease) bool {
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

// EnvironmentJobIsSelfOrDescendantOf walks jobID's StartedByJobID chain looking for ancestorID.
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
