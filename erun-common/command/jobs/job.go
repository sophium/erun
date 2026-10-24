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
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// A job is an activity lease plus an outcome. The outcome is captured as data
// by the process that ran the work; liveness is a probe of a recorded pid, never
// a command-line match; an answer is definite or explicitly unknown, never success.

const (
	EnvironmentJobStateRunning = "running"

	// UnknownReason* kinds are what callers branch on instead of parsing Reason.

	// UnknownReasonPodReplaced: the recorded hostname (pod name) differs from
	// the reader's, so the pod was recreated under the work.
	UnknownReasonPodReplaced = "pod-replaced"
	// UnknownReasonContainerRestarted: the same pod reports a container
	// termination at or after the job was last alive.
	UnknownReasonContainerRestarted = "container-restarted"
	// UnknownReasonSupervisorGone: same pod, supervisor gone, and no checkable cause.
	UnknownReasonSupervisorGone = "supervisor-gone"
	// UnknownReasonAttachedProcessGone: an attached pid had no erun supervisor to see its exit.
	UnknownReasonAttachedProcessGone = "attached-process-gone"

	// DefaultEnvironmentJobOutputLimitBytes caps captured output; the outcome
	// never comes from the log, so the cap costs only detail.
	DefaultEnvironmentJobOutputLimitBytes int64 = 16 << 20

	// DefaultEnvironmentJobAwaitTimeout is short so callers poll rather than park.
	DefaultEnvironmentJobAwaitTimeout = 30 * time.Second
	// MaxEnvironmentJobAwaitTimeout bounds one await; callers wait longer by calling again.
	MaxEnvironmentJobAwaitTimeout = 10 * time.Minute

	// EnvironmentJobRetention keeps a finished job readable for a reconnecting orchestrator.
	EnvironmentJobRetention = 24 * time.Hour
	// EnvironmentJobHistoryCap bounds finished records kept per environment.
	EnvironmentJobHistoryCap = 50

	// EnvironmentJobAliveHeartbeatInterval is independent of output, so a
	// legitimately silent job still proves its supervisor is alive.
	EnvironmentJobAliveHeartbeatInterval = 1 * time.Second
	// EnvironmentJobAliveStaleMs: past this AliveAgeMs, treat the job as failed
	// with an unknown outcome.
	EnvironmentJobAliveStaleMs int64 = 5000

	// DefaultEnvironmentJobOutputReadBytes bounds one output read to a page.
	DefaultEnvironmentJobOutputReadBytes int64 = 64 << 10
	environmentJobLeasePrefix                  = "job-"

	// EnvironmentJobGateIncompleteWaitCapEnvVar overrides the wait cap for
	// subprocess tests that cannot swap the var.
	EnvironmentJobGateIncompleteWaitCapEnvVar = "ERUN_JOB_GATE_INCOMPLETE_WAIT_CAP"
	// EnvironmentJobGateIncompletePollEnvVar overrides the poll interval likewise.
	EnvironmentJobGateIncompletePollEnvVar = "ERUN_JOB_GATE_INCOMPLETE_POLL"
)

// environmentJobGateIncompleteWaitCap bounds how long a finish check waits for
// a started job's verdict. It is generous because the supervisor is already
// detached; a var so tests can shrink it.
var environmentJobGateIncompleteWaitCap = EnvironmentJobRetention

var environmentJobGateIncompletePoll = 2 * time.Second

func resolveEnvironmentJobGateIncompleteWaitCap() time.Duration {
	return resolveEnvironmentJobDurationOverride(EnvironmentJobGateIncompleteWaitCapEnvVar, environmentJobGateIncompleteWaitCap)
}

func resolveEnvironmentJobGateIncompletePoll() time.Duration {
	return resolveEnvironmentJobDurationOverride(EnvironmentJobGateIncompletePollEnvVar, environmentJobGateIncompletePoll)
}

// environmentJobMaxReinvocations is small on purpose: each follow-up turn is a
// real LLM invocation. A var so tests can shrink it.
var environmentJobMaxReinvocations = 2

// environmentJobReinvocationBudget bounds the chain's wall-clock time, which
// the count cap alone does not.
var environmentJobReinvocationBudget = 30 * time.Minute

const (
	// EnvironmentJobMaxReinvocationsEnvVar overrides the reinvocation count;
	// invalid values fall back to the default.
	EnvironmentJobMaxReinvocationsEnvVar = "ERUN_JOB_MAX_REINVOCATIONS"
	// EnvironmentJobReinvocationBudgetEnvVar overrides the wall-clock budget.
	EnvironmentJobReinvocationBudgetEnvVar = "ERUN_JOB_REINVOCATION_BUDGET"
)

// EnvironmentJobMaxReinvocations returns the enforced bound, so displays cannot drift from it.
func EnvironmentJobMaxReinvocations() int {
	return resolveEnvironmentJobMaxReinvocations()
}

func resolveEnvironmentJobMaxReinvocations() int {
	if raw := strings.TrimSpace(os.Getenv(EnvironmentJobMaxReinvocationsEnvVar)); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			return parsed
		}
	}
	return environmentJobMaxReinvocations
}

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

// environmentJobSucceeded is the only definition of success: a zero outcome
// with no leftover processes, no running or failed started jobs, and a clean
// working tree. An unknown job never succeeds.
func environmentJobSucceeded(j shared.EnvironmentJob) bool {
	return j.State == shared.EnvironmentJobStateExited && j.ExitCode != nil && *j.ExitCode == 0 &&
		!j.WorktreeDirty && j.StartedJobFailed == ""
}

// LoadEnvironmentJob returns one job with its state resolved, persisting any demotion.
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

// LoadEnvironmentJobs returns every retained job, newest first, pruning aged-out records.
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
			// An unreadable record would otherwise stay in every listing forever.
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

// reconcileEnvironmentJob believes "running" only while the recorded pid is
// alive; otherwise the demotion to unknown is persisted.
func reconcileEnvironmentJob(dir string, job shared.EnvironmentJob, now time.Time, alive func(int) bool, hostname string) shared.EnvironmentJob {
	return reconcileEnvironmentJobWithRestartCheck(dir, job, now, alive, hostname, shared.RunOpenKubectl)
}

func reconcileEnvironmentJobWithRestartCheck(dir string, job shared.EnvironmentJob, now time.Time, alive func(int) bool, hostname string, restartRunner shared.OpenKubectlRunnerFunc) shared.EnvironmentJob {
	// Never persisted: it is meaningless without the read time.
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
	// Re-read: the supervisor writes its outcome and then exits, so the record
	// read above may be stale and must not be overwritten.
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
		// No hostname to compare, so neither replacement nor restart can be confirmed.
		job.UnknownReasonKind = UnknownReasonSupervisorGone
		job.Reason = fmt.Sprintf("job supervisor %d is gone without recording an exit status; this job predates hostname tracking, so whether the pod was replaced could not be determined", job.PID)
	}
	return job
}

// reconcileSamePodUnknownReason credits a container restart only when it lands
// at or after the job was last alive.
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

// environmentJobResourceStateSummaryFunc reports cgroup numbers, never a
// cause; a var so tests avoid the live cgroup.
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

// environmentJobChildren returns parentID's started jobs, reconciled like every other read.
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

// environmentJobRunningChildren excludes handed-off jobs, which are meant to outlive their parent.
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

// environmentJobFailedChildren excludes handed-off jobs and failures superseded
// by a later same-named attempt, since a rerun gets a fresh id under one --name.
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

func environmentJobCountsAsFailedChild(child shared.EnvironmentJob, parentID string, latestStartByName map[string]time.Time) bool {
	if child.Handoff || !child.Finished() || child.Succeeded {
		return false
	}
	// Only the parent that cancelled the child may read the cancel as expected.
	if child.CancelledByJobID != "" && child.CancelledByJobID == parentID {
		return false
	}
	if child.Name != "" && child.StartedAt.Before(latestStartByName[child.Name]) {
		return false
	}
	return true
}

// environmentJobAliveAgeMs is nil only before the first beat, so "no signal"
// is never read as zero.
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

// pruneEnvironmentJobs keeps finished jobs for EnvironmentJobRetention, at most
// EnvironmentJobHistoryCap of them; running jobs are never pruned.
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

// AwaitEnvironmentJobResult separates "not finished yet" from every outcome.
type AwaitEnvironmentJobResult struct {
	Job shared.EnvironmentJob `json:"job"`
	// TimedOut is never true for a job that failed.
	TimedOut       bool  `json:"timedOut"`
	WaitedSeconds  int64 `json:"waitedSeconds"`
	TimeoutSeconds int64 `json:"timeoutSeconds"`
}

// AwaitEnvironmentJob waits for a terminal state, bounded by the caller's timeout.
func AwaitEnvironmentJob(params model.AwaitEnvironmentJobParams) (AwaitEnvironmentJobResult, error) {
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

// writeEnvironmentJob writes atomically: a status read can land mid-write.
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

// environmentJobCancelMarkerPath records who cancelled a job, so its supervisor,
// the record's only writer, folds the provenance in.
func environmentJobCancelMarkerPath(dir, id string) string {
	return filepath.Join(dir, id+".cancelled-by")
}

// recordEnvironmentJobCancelRequest is best-effort: failure costs only the provenance.
func recordEnvironmentJobCancelRequest(dir, id, cancelledBy string) {
	_ = os.WriteFile(environmentJobCancelMarkerPath(dir, id), []byte(cancelledBy), 0o644)
}

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

// EnvironmentJobIDFromLeaseID recovers the job a lease is held for, if any.
func EnvironmentJobIDFromLeaseID(leaseID string) (string, bool) {
	id := strings.TrimPrefix(leaseID, environmentJobLeasePrefix)
	if id == leaseID || strings.TrimSpace(id) == "" {
		return "", false
	}
	return id, true
}
