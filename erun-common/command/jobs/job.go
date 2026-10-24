package jobs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// A job is an activity lease plus an outcome. The lease already records what is
// keeping an environment busy; what it cannot answer is "did it finish, and with
// what result". Answering that through a general-purpose escape hatch means
// re-implementing detachment, log redirection, polling, and exit-status capture
// in shell for every caller — which is where the observation bugs come from, not
// from the work itself.
//
// Three rules follow from those bugs and shape everything below.
//
// The outcome is captured by the process that ran the work, inside the
// environment, and stored as data. No sentinel token in a log, no exit code
// expanded by whichever shell happened to wrap the call.
//
// Liveness is a probe of a recorded pid, never a match against a command line.
// A pattern can match the process doing the observing — the polling loop, the
// caller's own shell — and two of those bugs were exactly that.
//
// An answer is definite or explicitly unknown, never silently partial. A job
// whose supervisor vanished without recording an outcome reads as unknown; it
// never reads as success, and it never disappears.

const (
	// EnvironmentJobStateRunning means the supervisor is alive and the work has
	// not reported an outcome yet.
	EnvironmentJobStateRunning = "running"

	// UnknownReasonKind values are what an orchestrator branches on instead of
	// pattern-matching the free-text Reason. Each names a distinct,
	// attributable cause for a job that ended in EnvironmentJobStateUnknown.
	//
	// UnknownReasonPodReplaced is certain, not a guess: the job recorded the
	// hostname of the pod that started it (a Kubernetes pod's hostname is its
	// pod name, unique per pod instance), and the hostname reading this record
	// back does not match. The runtime pod was recreated out from under the
	// work — most often eviction (e.g. a release filling the node's disk) or an
	// operator-triggered redeploy/restart — and the work is gone with it.
	UnknownReasonPodReplaced = "pod-replaced"
	// UnknownReasonContainerRestarted is certain, not a guess: the same pod
	// answered a live `kubectl get pod` with lastState.terminated for the
	// runtime container, naming a reason and exit code that lands at or after
	// this job was last known alive. The pod was not replaced; its container
	// was killed and Kubernetes restarted it in place, taking the supervisor
	// process with it.
	UnknownReasonContainerRestarted = "container-restarted"
	// UnknownReasonSupervisorGone means the same pod is still running, and
	// nothing checkable explains why the supervisor process the job recorded
	// is not: either the Kubernetes API could not be reached to check for a
	// container restart, or it answered and found none attributable to this
	// job. Distinct from a pod replacement (the environment survived) and
	// from a container restart (a checked, named cause) alike -- this is the
	// genuinely-unknown case, and the reason says so instead of guessing.
	UnknownReasonSupervisorGone = "supervisor-gone"
	// UnknownReasonAttachedProcessGone means the job was never started by
	// erun — it tracks a pid the caller named — so there was never a
	// supervisor in a position to observe an exit status, pod replacement or
	// not.
	UnknownReasonAttachedProcessGone = "attached-process-gone"

	// DefaultEnvironmentJobOutputLimitBytes bounds one job's captured output so a
	// chatty run cannot fill the environment's home volume. The outcome never
	// comes from the log, so hitting the cap costs detail, never the result. A
	// long agent run in stream-json mode blows through 4 MiB well under an hour,
	// so the default is wide enough to cover most single-session runs; progress
	// itself no longer depends on this cap at all (agentProgressReader is fed
	// directly from the process's own writes).
	DefaultEnvironmentJobOutputLimitBytes int64 = 16 << 20

	// DefaultEnvironmentJobAwaitTimeout is short enough that a caller polls
	// rather than parks on a connection.
	DefaultEnvironmentJobAwaitTimeout = 30 * time.Second
	// MaxEnvironmentJobAwaitTimeout is the ceiling that makes "bounded" real. A
	// caller that wants to wait longer calls await again; holding one call open
	// for a job's lifetime is the failure mode this surface replaces.
	MaxEnvironmentJobAwaitTimeout = 10 * time.Minute

	// EnvironmentJobRetention keeps a finished job readable long enough for an
	// orchestrator that reconnects — after a dropped transport, a desktop
	// restart, or overnight — to still learn the outcome. Reaping at exit would
	// recreate the problem this closes.
	EnvironmentJobRetention = 24 * time.Hour
	// EnvironmentJobHistoryCap bounds the finished records kept per environment
	// so a caller starting many short jobs cannot grow the store without limit.
	EnvironmentJobHistoryCap = 50

	// EnvironmentJobAliveHeartbeatInterval is the fixed cadence the supervisor
	// stamps LastAliveAt/AliveSeq at, independent of the activity lease's much
	// coarser renewal interval (300s at the default TTL) and of an agent job's
	// 2s progress fold. It does not depend on the work producing output, which
	// is what lets it answer "is the supervisor still there" for a command job
	// that is legitimately silent for minutes (an image pull, a slow test).
	EnvironmentJobAliveHeartbeatInterval = 1 * time.Second
	// EnvironmentJobAliveStaleMs is the documented caller rule: once AliveAgeMs
	// exceeds this, stop waiting and treat the job as failed with an unknown
	// outcome. It is 5x EnvironmentJobAliveHeartbeatInterval — headroom for poll
	// jitter and scheduling delay, not for the beat itself ever being late by
	// design.
	EnvironmentJobAliveStaleMs int64 = 5000

	// DefaultEnvironmentJobOutputReadBytes bounds one output read so a poll
	// returns a page rather than the whole log.
	DefaultEnvironmentJobOutputReadBytes int64 = 64 << 10
	environmentJobLeasePrefix                  = "job-"

	// EnvironmentJobGateIncompleteWaitCapEnvVar overrides
	// environmentJobGateIncompleteWaitCap for a single job supervisor process,
	// parsed with time.ParseDuration; an empty or unparseable value falls back
	// to the default. It exists because the default is generous on purpose
	// (see environmentJobGateIncompleteWaitCap) and a subprocess-level
	// integration test has no in-process var to swap the way a Go unit test
	// does -- the same shape as ERUN_RELEASE_MIN_DISK_HEADROOM_BYTES tuning a
	// different generous-by-default floor.
	EnvironmentJobGateIncompleteWaitCapEnvVar = "ERUN_JOB_GATE_INCOMPLETE_WAIT_CAP"
	// EnvironmentJobGateIncompletePollEnvVar is EnvironmentJobGateIncompleteWaitCapEnvVar's
	// twin for the poll interval, same parsing and fallback rule.
	EnvironmentJobGateIncompletePollEnvVar = "ERUN_JOB_GATE_INCOMPLETE_POLL"
)

// environmentJobGateIncompleteWaitCap bounds how long a job's own finish check
// waits for a non-handoff job it started to reach a verdict before giving up
// and recording gate-incomplete anyway. It is generous by design: unlike an
// interactive `job await` call, nothing is holding a connection or a caller's
// turn open here — the supervisor is already a detached background process,
// so waiting out even the longest gate costs nothing the await ceiling above
// exists to avoid. It shares EnvironmentJobRetention's value only because both
// answer "how long is worth waiting before giving up", not because the two
// concerns are the same one. A var, not a const, so a Go test can shrink it
// directly rather than genuinely waiting out a seeded "still running" child;
// resolveEnvironmentJobGateIncompleteWaitCap is what a subprocess (an
// integration test, or an operator) overrides instead, via
// EnvironmentJobGateIncompleteWaitCapEnvVar.
var environmentJobGateIncompleteWaitCap = EnvironmentJobRetention

// environmentJobGateIncompletePoll is how often the wait re-reads the started
// job's record while it waits. A var for the same test-shrinking reason as
// environmentJobGateIncompleteWaitCap above.
var environmentJobGateIncompletePoll = 2 * time.Second

// resolveEnvironmentJobGateIncompleteWaitCap is what resolveEnvironmentJobOutcome
// actually waits by: EnvironmentJobGateIncompleteWaitCapEnvVar when it is set
// to a valid positive duration, otherwise environmentJobGateIncompleteWaitCap.
func resolveEnvironmentJobGateIncompleteWaitCap() time.Duration {
	return resolveEnvironmentJobDurationOverride(EnvironmentJobGateIncompleteWaitCapEnvVar, environmentJobGateIncompleteWaitCap)
}

// resolveEnvironmentJobGateIncompletePoll is environmentJobGateIncompletePoll's
// EnvironmentJobGateIncompletePollEnvVar-aware twin.
func resolveEnvironmentJobGateIncompletePoll() time.Duration {
	return resolveEnvironmentJobDurationOverride(EnvironmentJobGateIncompletePollEnvVar, environmentJobGateIncompletePoll)
}

// environmentJobMaxReinvocations bounds how many bounded follow-up turns an
// agent job's own supervisor will run in response to the job it started coming
// back incomplete or failed (see decideEnvironmentJobReinvocation). It is small
// on purpose: this is a safety-bounded retry of a scoped, already-observed
// failure, not a general "keep trying" loop, and each turn is a real LLM
// invocation with its own cost and its own chance of causing something new. A
// var so a test can shrink it without genuinely burning reinvocations; an
// operator overrides via EnvironmentJobMaxReinvocationsEnvVar the same way
// resolveEnvironmentJobGateIncompleteWaitCap works.
var environmentJobMaxReinvocations = 2

// environmentJobReinvocationBudget caps the wall-clock time a chain of
// reinvocations may spend in total, independently of environmentJobMaxReinvocations:
// a count cap alone does not bound cost if the model can spend an unbounded
// amount of time (and, on some providers, budget) inside each turn. The two
// bounds are deliberately different shapes -- one bounds how many extra
// inferences can run, the other bounds how long the whole chain may keep the
// environment busy -- so either one exhausting stops the chain.
var environmentJobReinvocationBudget = 30 * time.Minute

const (
	// EnvironmentJobMaxReinvocationsEnvVar overrides environmentJobMaxReinvocations
	// for a single job supervisor process; an empty or unparseable (non-integer,
	// or negative) value falls back to the default. Same shape as
	// EnvironmentJobGateIncompleteWaitCapEnvVar, for the same reason: an
	// integration test needs to shrink or grow this without a package-level Go
	// test's ability to swap the var directly.
	EnvironmentJobMaxReinvocationsEnvVar = "ERUN_JOB_MAX_REINVOCATIONS"
	// EnvironmentJobReinvocationBudgetEnvVar is EnvironmentJobMaxReinvocationsEnvVar's
	// twin for the wall-clock budget, parsed with time.ParseDuration.
	EnvironmentJobReinvocationBudgetEnvVar = "ERUN_JOB_REINVOCATION_BUDGET"
)

// EnvironmentJobMaxReinvocations returns the bound a caller renders alongside
// EnvironmentJob.ReinvocationCount (e.g. `job status`'s "resumed N/M" suffix),
// so the displayed bound can never drift from the one actually enforced.
func EnvironmentJobMaxReinvocations() int {
	return resolveEnvironmentJobMaxReinvocations()
}

// resolveEnvironmentJobMaxReinvocations is environmentJobMaxReinvocations,
// EnvironmentJobMaxReinvocationsEnvVar-aware.
func resolveEnvironmentJobMaxReinvocations() int {
	if raw := strings.TrimSpace(os.Getenv(EnvironmentJobMaxReinvocationsEnvVar)); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			return parsed
		}
	}
	return environmentJobMaxReinvocations
}

// resolveEnvironmentJobReinvocationBudget is environmentJobReinvocationBudget's
// EnvironmentJobReinvocationBudgetEnvVar-aware twin.
func resolveEnvironmentJobReinvocationBudget() time.Duration {
	return resolveEnvironmentJobDurationOverride(EnvironmentJobReinvocationBudgetEnvVar, environmentJobReinvocationBudget)
}

func resolveEnvironmentJobDurationOverride(envVar string, fallback time.Duration) time.Duration {
	if raw := strings.TrimSpace(os.Getenv(envVar)); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

// environmentJobSucceeded is the only definition of success: an outcome was
// captured and it was zero, with nothing left running behind it and nothing
// left uncommitted in its own working tree. An unknown job is never a
// success, and neither is an abandoned or gate-incomplete one — a zero exit
// code from the process that started work it never waited for, whether that
// work is an unreaped process in its own group or a sibling job record, is
// not the same claim as a zero exit code from a job that finished cleanly. A
// dirty working tree is the same shape of not-actually-clean finish: whatever
// the supervisor managed to preserve on the agent's behalf (see
// job_worktree.go), the agent itself did not commit its own work, and that is
// worth a caller's attention even when everything else about the run looks
// fine. StartedJobFailed is the same shape again, one step removed: a job
// this job started and waited for reached a verdict, and the verdict was not
// success. Backs the EnvironmentJob.Succeeded field.
func environmentJobSucceeded(j shared.EnvironmentJob) bool {
	return j.State == shared.EnvironmentJobStateExited && j.ExitCode != nil && *j.ExitCode == 0 &&
		!j.WorktreeDirty && j.StartedJobFailed == ""
}

// LoadEnvironmentJob returns one job with its state resolved, reconciling and
// persisting an abandoned record on the way out.
func LoadEnvironmentJob(tenant, environment, id string, now time.Time) (shared.EnvironmentJob, error) {
	dir, err := shared.EnvironmentJobDir(tenant, environment)
	if err != nil {
		return shared.EnvironmentJob{}, err
	}
	resolved, err := resolveEnvironmentJobID(id)
	if err != nil {
		return shared.EnvironmentJob{}, err
	}
	job, err := shared.ReadEnvironmentJob(filepath.Join(dir, resolved+".json"))
	if err != nil {
		if os.IsNotExist(err) {
			return shared.EnvironmentJob{}, fmt.Errorf("no job %q in %s/%s", resolved, tenant, environment)
		}
		return shared.EnvironmentJob{}, err
	}
	return reconcileEnvironmentJob(dir, job, normalizeJobNow(now), activity.ProcessAlive, build.CurrentJobHostname()), nil
}

// LoadEnvironmentJobs returns every retained job, newest first, pruning records
// that aged out as it reads.
func LoadEnvironmentJobs(tenant, environment string, now time.Time) ([]shared.EnvironmentJob, error) {
	return loadEnvironmentJobs(tenant, environment, normalizeJobNow(now), activity.ProcessAlive, build.CurrentJobHostname())
}

func loadEnvironmentJobs(tenant, environment string, now time.Time, alive func(int) bool, hostname string) ([]shared.EnvironmentJob, error) {
	dir, err := shared.EnvironmentJobDir(tenant, environment)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	jobs := make([]shared.EnvironmentJob, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		job, err := shared.ReadEnvironmentJob(filepath.Join(dir, entry.Name()))
		if err != nil {
			// A record we cannot read cannot answer anything, and leaving it would
			// keep an unreadable job in every listing forever.
			removeEnvironmentJobFiles(dir, strings.TrimSuffix(entry.Name(), ".json"))
			continue
		}
		jobs = append(jobs, reconcileEnvironmentJob(dir, job, now, alive, hostname))
	}
	jobs = pruneEnvironmentJobs(dir, jobs, now)
	sort.Slice(jobs, func(i, j int) bool {
		if !jobs[i].StartedAt.Equal(jobs[j].StartedAt) {
			return jobs[i].StartedAt.After(jobs[j].StartedAt)
		}
		return jobs[i].ID < jobs[j].ID
	})
	return jobs, nil
}

// reconcileEnvironmentJob is the whole liveness rule. A record that still claims
// to be running is only believed while the process it named is alive; otherwise
// the job is demoted to unknown and that demotion is persisted, so every later
// read gives the same definite answer.
func reconcileEnvironmentJob(dir string, job shared.EnvironmentJob, now time.Time, alive func(int) bool, hostname string) shared.EnvironmentJob {
	return reconcileEnvironmentJobWithRestartCheck(dir, job, now, alive, hostname, shared.RunOpenKubectl)
}

// reconcileEnvironmentJobWithRestartCheck is reconcileEnvironmentJob with the
// Kubernetes restart lookup injectable, so a test can supply a fake pod
// response instead of shelling out to a real kubectl.
func reconcileEnvironmentJobWithRestartCheck(dir string, job shared.EnvironmentJob, now time.Time, alive func(int) bool, hostname string, restartRunner shared.OpenKubectlRunnerFunc) shared.EnvironmentJob {
	// Computed fresh on every read, in the reader's own now, and never
	// persisted: a stale value on disk would be meaningless without the read
	// time that produced it, exactly like OutputBytes below.
	job.AliveAgeMs = environmentJobAliveAgeMs(job.LastAliveAt, now)
	if job.State != EnvironmentJobStateRunning {
		job.Succeeded = environmentJobSucceeded(job)
		return job
	}
	if job.PID > 0 && alive != nil && alive(job.PID) {
		job.OutputBytes = environmentJobOutputSize(job.LogPath, job.OutputBytes)
		job.Succeeded = environmentJobSucceeded(job)
		return job
	}
	// The record above was read before this liveness check, so it can still
	// say "running" for a supervisor that finished in between: the supervisor
	// writes its terminal state and only then exits. Re-read before retiring
	// the job, so the demotion below can never overwrite an outcome that
	// landed while this check was being made -- and, because a supervisor
	// that really did die without recording still reads back as running here,
	// a genuinely lost job is demoted exactly as it was before.
	if fresh, err := shared.ReadEnvironmentJob(filepath.Join(dir, job.ID+".json")); err == nil && fresh.State != EnvironmentJobStateRunning {
		fresh.AliveAgeMs = environmentJobAliveAgeMs(fresh.LastAliveAt, now)
		fresh.OutputBytes = environmentJobOutputSize(fresh.LogPath, fresh.OutputBytes)
		fresh.Succeeded = environmentJobSucceeded(fresh)
		return fresh
	}
	job = demoteEnvironmentJobToUnknown(job, now, hostname, restartRunner)
	job.OutputBytes = environmentJobOutputSize(job.LogPath, job.OutputBytes)
	job.Succeeded = environmentJobSucceeded(job)
	_ = writeEnvironmentJob(dir, job)
	return job
}

// demoteEnvironmentJobToUnknown fills in the State/UnknownReasonKind/Reason a
// job gets once its supervisor is confirmed gone: attached work was never in
// a position to observe an exit, a hostname mismatch is a definite pod
// replacement, a hostname match rules that out and defers to
// reconcileSamePodUnknownReason, and no hostname at all leaves nothing to
// compare against.
func demoteEnvironmentJobToUnknown(job shared.EnvironmentJob, now time.Time, hostname string, restartRunner shared.OpenKubectlRunnerFunc) shared.EnvironmentJob {
	job.State = shared.EnvironmentJobStateUnknown
	job.EndedAt = now
	job.ExitCode = nil
	switch {
	case job.Attached:
		job.UnknownReasonKind = UnknownReasonAttachedProcessGone
		job.Reason = fmt.Sprintf("attached process %d is gone; erun did not run this work, so no exit status was recorded", job.PID)
	case job.Hostname != "" && job.Hostname != hostname:
		job.UnknownReasonKind = UnknownReasonPodReplaced
		job.Reason = fmt.Sprintf("job supervisor %d is gone without recording an exit status; the runtime pod was replaced (started on %q, this is %q)", job.PID, job.Hostname, hostname)
	case job.Hostname != "" && job.Hostname == hostname:
		job.UnknownReasonKind, job.Reason = reconcileSamePodUnknownReason(job, hostname, restartRunner)
	default:
		// This job predates hostname tracking, or hostname resolution failed on
		// both ends: there is nothing to compare against, so neither a
		// replacement nor a restart can be confirmed or ruled out.
		job.UnknownReasonKind = UnknownReasonSupervisorGone
		job.Reason = fmt.Sprintf("job supervisor %d is gone without recording an exit status; this job predates hostname tracking, so whether the pod was replaced could not be determined", job.PID)
	}
	return job
}

// reconcileSamePodUnknownReason handles the one case where a replacement is
// ruled out (hostname matches, so this is definitely the same pod): it asks
// Kubernetes whether the runtime container itself restarted, and only credits
// that restart when its timestamp lands at or after this job was last known
// alive -- a checked, named cause beats a guessed one, but only when it is
// actually attributable to this job rather than some earlier, unrelated one.
func reconcileSamePodUnknownReason(job shared.EnvironmentJob, hostname string, restartRunner shared.OpenKubectlRunnerFunc) (kind, reason string) {
	restarted, terminatedReason, exitCode, finishedAt, ok := jobSupervisorContainerRestart(hostname, config.DevopsComponentName, restartRunner)
	if !ok || !restarted || finishedAt.Before(jobLastKnownAliveAt(job)) {
		reason := fmt.Sprintf("job supervisor %d is gone without recording an exit status; the pod was not replaced (same hostname), and why the supervisor process ended could not be determined", job.PID)
		if summary := environmentJobResourceStateSummaryFunc(); summary != "" {
			reason += ". " + summary
		}
		return UnknownReasonSupervisorGone, reason
	}
	if terminatedReason == "" {
		terminatedReason = "no reason reported"
	}
	return UnknownReasonContainerRestarted, fmt.Sprintf("job supervisor %d is gone without recording an exit status; the %s container restarted (%s, exit code %d) at %s -- the pod itself was not replaced", job.PID, config.DevopsComponentName, terminatedReason, exitCode, finishedAt.UTC().Format(time.RFC3339))
}

// environmentJobResourceStateSummary renders this pod's own cgroup state at
// read time, for a job whose supervisor vanished with nothing else to
// explain why (see reconcileSamePodUnknownReason): a supervisor that died
// while pinned against its memory ceiling or heavily CPU-throttled is a
// different, more actionable finding than "could not be determined", and
// this is what stops erun sitting inside a cgroup that already has that
// answer and never looking. It never claims a cause -- only reports the
// numbers -- because the read happens after the fact and cannot prove what
// state the cgroup was actually in at the moment the supervisor died. "" when
// nothing here is readable (no cgroup v2, an unlimited container), so a
// caller never appends an empty clause.
// environmentJobResourceStateSummaryFunc is what reconcileSamePodUnknownReason
// actually calls; a var, matching environmentJobGateIncompleteWaitCap's
// established test-seam shape in this file, so a test can substitute a
// fixture reading instead of depending on this test process's own live
// cgroup (which, running inside `go test`, was never given the resource
// limits a real runtime pod has).
var environmentJobResourceStateSummaryFunc = environmentJobResourceStateSummary

func environmentJobResourceStateSummary() string {
	usage := config.ReadLocalRuntimeUsage(config.DefaultCgroupRoot)
	var parts []string
	if usage.Memory.Unavailable == "" && !usage.Memory.Unlimited && usage.Memory.LimitBytes > 0 {
		part := fmt.Sprintf("memory %.2f/%.2f GiB", gibibytes(usage.Memory.CurrentBytes), gibibytes(usage.Memory.LimitBytes))
		var details []string
		if usage.Memory.CeilingHitsObserved {
			details = append(details, fmt.Sprintf("ceiling reached %d times", usage.Memory.CeilingHits))
		}
		if usage.Memory.OOMKillsObserved {
			details = append(details, fmt.Sprintf("%d OOM kill(s)", usage.Memory.OOMKills))
		}
		if len(details) > 0 {
			part += ", " + strings.Join(details, ", ")
		}
		parts = append(parts, part)
	}
	if usage.CPU.Unavailable == "" && usage.CPU.Periods > 0 {
		percent := 100 * float64(usage.CPU.ThrottledPeriods) / float64(usage.CPU.Periods)
		parts = append(parts, fmt.Sprintf("CPU throttled in %.0f%% of periods (%d/%d)", percent, usage.CPU.ThrottledPeriods, usage.CPU.Periods))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Environment resource state: " + strings.Join(parts, "; ")
}

func gibibytes(bytes int64) float64 {
	return float64(bytes) / (1 << 30)
}

// environmentJobChildren returns every job in dir that names parentID as its
// StartedByJobID, reconciled against current process liveness the same way
// every other read is — so a child whose own supervisor died without
// recording an outcome reads as unknown here too, not as still running. Both
// environmentJobRunningChildren and environmentJobFailedChildren derive from
// this one listing so they agree on what counts as "a job I started" without
// needing the parent to have tracked its children itself.
func environmentJobChildren(dir, parentID string, now time.Time) []shared.EnvironmentJob {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	hostname := build.CurrentJobHostname()
	var children []shared.EnvironmentJob
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		job, err := shared.ReadEnvironmentJob(filepath.Join(dir, entry.Name()))
		if err != nil || job.StartedByJobID != parentID {
			continue
		}
		children = append(children, reconcileEnvironmentJob(dir, job, now, activity.ProcessAlive, hostname))
	}
	sort.Slice(children, func(i, j int) bool { return children[i].ID < children[j].ID })
	return children
}

// environmentJobRunningChildren returns parentID's children that are still not
// finished, excluding any marked Handoff: a deliberately handed-off job is
// meant to outlive whatever started it, so it must never be what keeps a
// parent's own finish check waiting. This is what lets a job's own finish
// check tell "work I started is still going" from a plain directory listing.
func environmentJobRunningChildren(dir, parentID string, now time.Time) []shared.EnvironmentJob {
	var running []shared.EnvironmentJob
	for _, child := range environmentJobChildren(dir, parentID, now) {
		if child.Handoff {
			continue
		}
		if !child.Finished() {
			running = append(running, child)
		}
	}
	return running
}

// environmentJobFailedChildren returns parentID's children that have finished
// without succeeding, excluding any marked Handoff, and excluding a failure a
// later same-named attempt has since superseded. It is what lets a parent
// whose own process exited cleanly still report the truth once it has waited
// out a job it started (see EnvironmentJobStateGateIncomplete) and that job
// turns out not to have succeeded — a clean exit code from the parent's own
// process is not the same claim as a clean outcome from work it waited for.
//
// The supersede rule matters because a name is not a one-shot id: a caller
// that folds the tree and command into --id gives an agent that fixes what a
// gate found and reruns it a fresh id under the same --name, and the earlier
// failing attempt's record is never replaced (reserveEnvironmentJobID only
// replaces an id that is reused outright). Without this, a parent would name
// that stale failure forever — including once a later attempt under the same
// name went green, reporting exited/succeeded next to a startedJobFailed
// naming a different job than the one that had actually passed.
func environmentJobFailedChildren(dir, parentID string, now time.Time) []shared.EnvironmentJob {
	children := environmentJobChildren(dir, parentID, now)
	latestStartByName := make(map[string]time.Time, len(children))
	for _, child := range children {
		if child.Name == "" {
			continue
		}
		if child.StartedAt.After(latestStartByName[child.Name]) {
			latestStartByName[child.Name] = child.StartedAt
		}
	}
	var failed []shared.EnvironmentJob
	for _, child := range children {
		if environmentJobCountsAsFailedChild(child, parentID, latestStartByName) {
			failed = append(failed, child)
		}
	}
	return failed
}

// environmentJobCountsAsFailedChild is environmentJobFailedChildren's
// per-child inclusion rule, split out so each exclusion reads as one
// documented condition rather than folding into one long loop body.
func environmentJobCountsAsFailedChild(child shared.EnvironmentJob, parentID string, latestStartByName map[string]time.Time) bool {
	if child.Handoff || !child.Finished() || child.Succeeded {
		return false
	}
	// A child this same parent deliberately cancelled ended exactly as
	// asked -- that is the parent's own intent, not a failure of work it
	// waited for. A cancel issued by anyone else (a different job, or no
	// job at all) still counts: only the parent that asked for the stop
	// gets to read it as expected.
	if child.CancelledByJobID != "" && child.CancelledByJobID == parentID {
		return false
	}
	if child.Name != "" && child.StartedAt.Before(latestStartByName[child.Name]) {
		return false
	}
	return true
}

// environmentJobAliveAgeMs is nil only when the job has never beaten, so a
// caller never mistakes "no signal yet" for "beating zero milliseconds ago".
func environmentJobAliveAgeMs(lastAliveAt, now time.Time) *int64 {
	if lastAliveAt.IsZero() {
		return nil
	}
	age := now.Sub(lastAliveAt)
	if age < 0 {
		age = 0
	}
	ms := age.Milliseconds()
	return &ms
}

// pruneEnvironmentJobs enforces retention: a finished job stays readable for
// EnvironmentJobRetention so a reconnecting caller can still learn its outcome,
// and only the newest EnvironmentJobHistoryCap finished records are kept.
// Running jobs are never pruned.
func pruneEnvironmentJobs(dir string, jobs []shared.EnvironmentJob, now time.Time) []shared.EnvironmentJob {
	kept := make([]shared.EnvironmentJob, 0, len(jobs))
	finished := make([]shared.EnvironmentJob, 0, len(jobs))
	for _, job := range jobs {
		if !job.Finished() {
			kept = append(kept, job)
			continue
		}
		if !job.EndedAt.IsZero() && now.Sub(job.EndedAt) >= EnvironmentJobRetention {
			removeEnvironmentJobFiles(dir, job.ID)
			continue
		}
		finished = append(finished, job)
	}
	sort.Slice(finished, func(i, j int) bool {
		if !finished[i].EndedAt.Equal(finished[j].EndedAt) {
			return finished[i].EndedAt.After(finished[j].EndedAt)
		}
		return finished[i].ID < finished[j].ID
	})
	for index, job := range finished {
		if index >= EnvironmentJobHistoryCap {
			removeEnvironmentJobFiles(dir, job.ID)
			continue
		}
		kept = append(kept, job)
	}
	return kept
}

// AwaitEnvironmentJobParams bounds one wait. The timeout is the contract: the
// call returns either an outcome or "still running", never a held connection.
type AwaitEnvironmentJobParams struct {
	Tenant      string
	Environment string
	ID          string
	Timeout     time.Duration
	// Poll is how often the record is re-read; zero takes the default.
	Poll time.Duration
}

// AwaitEnvironmentJobResult separates "not finished yet" from every outcome, so
// a caller never has to infer one from the other.
type AwaitEnvironmentJobResult struct {
	Job shared.EnvironmentJob `json:"job"`
	// TimedOut is true only when the bounded wait elapsed with the job still
	// running. It is never true for a job that failed.
	TimedOut       bool  `json:"timedOut"`
	WaitedSeconds  int64 `json:"waitedSeconds"`
	TimeoutSeconds int64 `json:"timeoutSeconds"`
}

// AwaitEnvironmentJob waits for a job to reach a terminal state, bounded by the
// caller's timeout.
func AwaitEnvironmentJob(params AwaitEnvironmentJobParams) (AwaitEnvironmentJobResult, error) {
	timeout, err := normalizeEnvironmentJobAwaitTimeout(params.Timeout)
	if err != nil {
		return AwaitEnvironmentJobResult{}, err
	}
	poll := params.Poll
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	if poll > timeout {
		poll = timeout
	}
	started := time.Now()
	deadline := started.Add(timeout)
	result := AwaitEnvironmentJobResult{TimeoutSeconds: int64(timeout / time.Second)}
	for {
		job, err := LoadEnvironmentJob(params.Tenant, params.Environment, params.ID, time.Now())
		if err != nil {
			return AwaitEnvironmentJobResult{}, err
		}
		result.Job = job
		result.WaitedSeconds = int64(time.Since(started) / time.Second)
		if job.Finished() {
			return result, nil
		}
		if !time.Now().Before(deadline) {
			result.TimedOut = true
			return result, nil
		}
		remaining := time.Until(deadline)
		if remaining > poll {
			remaining = poll
		}
		time.Sleep(remaining)
	}
}

func normalizeEnvironmentJobAwaitTimeout(timeout time.Duration) (time.Duration, error) {
	if timeout == 0 {
		return DefaultEnvironmentJobAwaitTimeout, nil
	}
	if timeout < 0 {
		return 0, fmt.Errorf("await timeout must be greater than zero")
	}
	if timeout > MaxEnvironmentJobAwaitTimeout {
		return 0, fmt.Errorf("await timeout %s exceeds the %s ceiling; poll again instead of holding one call open", timeout, MaxEnvironmentJobAwaitTimeout)
	}
	return timeout, nil
}

func resolveEnvironmentJobID(id string) (string, error) {
	value := strings.TrimSpace(id)
	if value == "" {
		return "", fmt.Errorf("job id is required")
	}
	sanitized := core.SanitizeForFilename(value)
	if sanitized == "" || sanitized == "_" || sanitized == "." || sanitized == ".." {
		return "", fmt.Errorf("job id %q has no usable characters", value)
	}
	return sanitized, nil
}

// writeEnvironmentJob replaces the record atomically, because a status read can
// land between the supervisor's write and its rename; a half-written record
// would be exactly the silently-partial answer this store refuses to give.
func writeEnvironmentJob(dir string, job shared.EnvironmentJob) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, job.ID+".json")
	temp := path + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}

func removeEnvironmentJobFiles(dir, id string) {
	if strings.TrimSpace(id) == "" {
		return
	}
	_ = os.Remove(filepath.Join(dir, id+".json"))
	_ = os.Remove(environmentJobLogPath(dir, id))
	_ = os.Remove(environmentJobCancelMarkerPath(dir, id))
}

// environmentJobCancelMarkerPath is where CancelEnvironmentJob records who
// asked a live job to stop, so that job's own supervisor -- the only writer
// of its JSON record (see jobRecorder) -- can fold the provenance in once it
// finishes, rather than a second process racing the supervisor to write the
// same file.
func environmentJobCancelMarkerPath(dir, id string) string {
	return filepath.Join(dir, id+".cancelled-by")
}

// recordEnvironmentJobCancelRequest writes the canceller's own job id (empty
// when the cancel came from outside any job) just before the signal reaches
// the target, so the target's own finish check can later tell "my parent
// deliberately stopped me" from "I died for some other reason". Best-effort,
// matching the lease/heartbeat release pattern elsewhere in this package: a
// write that fails here costs only the provenance, not the cancel itself.
func recordEnvironmentJobCancelRequest(dir, id, cancelledBy string) {
	_ = os.WriteFile(environmentJobCancelMarkerPath(dir, id), []byte(cancelledBy), 0o644)
}

// consumeEnvironmentJobCancelRequest reads back and removes the marker
// recordEnvironmentJobCancelRequest left, so a job's own finish check folds
// the provenance in exactly once. Absent is the common case -- most jobs end
// on their own, never cancelled -- and reads as "", ok=false.
func consumeEnvironmentJobCancelRequest(dir, id string) (string, bool) {
	path := environmentJobCancelMarkerPath(dir, id)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	_ = os.Remove(path)
	return string(data), true
}

func normalizeJobNow(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now()
	}
	return now
}

func environmentJobLeaseID(id string) string {
	return environmentJobLeasePrefix + id
}

// EnvironmentJobIDFromLeaseID recovers the job a lease is held for, and reports
// false for a lease held by anything else. Callers that want to act on the job
// behind a lease need the inverse of the id scheme, and re-deriving the prefix
// at each call site is how the two drift apart.
func EnvironmentJobIDFromLeaseID(leaseID string) (string, bool) {
	id := strings.TrimPrefix(leaseID, environmentJobLeasePrefix)
	if id == leaseID || strings.TrimSpace(id) == "" {
		return "", false
	}
	return id, true
}
