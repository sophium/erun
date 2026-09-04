package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// InvestigateFailure captures a failure report to a host file and spawns a
// fresh, prompt-seeded orchestrator to fix it or improve error reporting. The
// report is delivered as a file the agent reads — a large report doesn't survive
// being typed at a prompt — and the seed prompt references its path plus the
// standing instruction to file an erun issue for platform bugs. The orchestrator
// is transient (not persisted), grouped under the failed env's tenant, and runs
// in that env's host workspace when one is synced.
//
// Spawning is bounded (see investigation_bounds.go): the report is always
// staged, and it becomes an agent only when it carries something to investigate,
// is not part of a failure event already being investigated, is not a repeat of
// a failure investigated recently, and the live population has room. A refusal
// is returned as an error so the operator is told which bound stopped it.
func (a *App) InvestigateFailure(report, tenant, environment string, cols, rows int) (orchestratorInfo, error) {
	name := "Investigate"
	if env := strings.TrimSpace(environment); env != "" {
		name = "Investigate " + env
	}
	return a.spawnFailureAgent("investigate", report, tenant, environment, cols, rows, name, func(path string) string {
		return fmt.Sprintf("A failure report is saved at %s. Read it, then plan to fix the issue, or improve error reporting so similar issues can be fixed faster. If this is an erun platform issue rather than a project issue, file a bug in the erun GitHub tracker (sophium/erun) using the erun-file-issue skill.", path)
	})
}

// spawnFailureAgent is the bounded-spawn plumbing shared by every agent a
// failure report can start — an investigation, or a bug-report draft
// (report_bug.go): stage the report, admit it against the shared
// investigation_bounds.go population, spawn the transient orchestrator, and
// register it as an environment job. idPrefix and prompt are the only things
// that differ between callers; prompt receives the staged report's path so it
// can be referenced from the seed text.
func (a *App) spawnFailureAgent(idPrefix, report, tenant, environment string, cols, rows int, name string, prompt func(reportPath string) string) (orchestratorInfo, error) {
	report = strings.TrimSpace(report)
	if report == "" {
		return orchestratorInfo{}, fmt.Errorf("nothing to work from: the failure report is empty")
	}
	path, err := a.investigations.stageReport(report)
	if err != nil {
		return orchestratorInfo{}, err
	}
	id := fmt.Sprintf("%s-%d", idPrefix, time.Now().UnixNano())
	if err := a.investigations.admit(id, report, tenant, environment); err != nil {
		// The refused report stays staged: it is the record of what was reported,
		// and for a report too thin to act on it is also the evidence of the
		// reporting gap that is the real bug.
		log.Printf("erun-app: %s refused for %s (%s): report=%s", idPrefix, investigationTargetLabel(tenant, environment), investigationRefusalReason(err), path)
		return orchestratorInfo{}, err
	}

	var envs []eruncommon.OrchestratorEnvConfig
	if t, env := strings.TrimSpace(tenant), strings.TrimSpace(environment); t != "" && env != "" {
		envs = []eruncommon.OrchestratorEnvConfig{
			{Tenant: t, Environment: env, Directory: a.investigateWorkingDir(t, env)},
		}
	}
	info, err := a.spawnOrchestratorSession(orchestratorSpawn{
		id:            id,
		name:          name,
		envs:          envs,
		initialPrompt: prompt(path),
		transient:     true,
		cols:          cols,
		rows:          rows,
	})
	if err != nil {
		a.investigations.discard(id)
		return orchestratorInfo{}, err
	}
	a.activateInvestigation(investigationRecord{
		ID:          id,
		Tenant:      strings.TrimSpace(tenant),
		Environment: strings.TrimSpace(environment),
		ReportPath:  path,
		Signature:   investigationSignature(report, tenant, environment),
		StartedAt:   a.investigations.startedAt(id),
	}, name)
	return info, nil
}

// activateInvestigation promotes the reserved slot to a running investigation
// and makes it visible where an operator and an agent already look: as a job on
// the failed environment, holding an activity lease for as long as it runs.
func (a *App) activateInvestigation(record investigationRecord, name string) {
	record.LogPath = a.investigations.writeInvestigationLog(record)
	record.LeaseID = a.attachInvestigationJob(record, name)
	a.investigations.activate(record.ID, record.ReportPath, record.LogPath, record.LeaseID)
}

// attachInvestigationJob registers the investigation as an attached job on the
// environment it is investigating. That is the surface job_status already reads,
// and the lease is what makes the environment report as busy while an agent it
// spawned is running — without it, an investigation spends the account with
// nothing anywhere saying so. Failure is non-fatal and logged: the investigation
// is already running, and losing its bookkeeping must not lose the work.
func (a *App) attachInvestigationJob(record investigationRecord, name string) string {
	if record.Tenant == "" || record.Environment == "" {
		return ""
	}
	pid := a.orchestratorSessionPID(record.ID)
	if pid <= 0 {
		log.Printf("erun-app: investigation %s has no pid to track; it will not appear as a job", record.ID)
		return ""
	}
	job, err := eruncommon.AttachEnvironmentJob(eruncommon.Context{}, eruncommon.AttachEnvironmentJobParams{
		Tenant:      record.Tenant,
		Environment: record.Environment,
		ID:          record.ID,
		Name:        name,
		PID:         pid,
		LogPath:     record.LogPath,
		LeaseTTL:    investigationLifetime,
	})
	if err != nil {
		log.Printf("erun-app: register investigation %s as a job on %s/%s: %v", record.ID, record.Tenant, record.Environment, err)
		return ""
	}
	return job.LeaseID
}

// finishExpiredInvestigation is the lifetime bound's effect: stop the session,
// say so in the investigation's log and to the operator, and release the lease
// so the environment stops reading as busy on account of an agent that is gone.
func (a *App) finishExpiredInvestigation(record investigationRecord) {
	a.stopOrchestratorSession(record.ID)
	a.investigations.appendInvestigationLog(record, fmt.Sprintf("terminated: still running after the %s lifetime bound without concluding; stopped by the desktop", investigationAge(investigationLifetime)))
	if record.Tenant != "" && record.Environment != "" && record.LeaseID != "" {
		if _, err := eruncommon.ReleaseEnvironmentActivityLease(record.Tenant, record.Environment, record.LeaseID); err != nil {
			log.Printf("erun-app: release investigation lease %s: %v", record.LeaseID, err)
		}
	}
	log.Printf("erun-app: investigation %s stopped after the %s lifetime bound", record.ID, investigationAge(investigationLifetime))
	a.emitAppNotification("warning", fmt.Sprintf("The investigation of %s was stopped after %s without concluding. Its report is at %s — read it, or start a fresh investigation now that the bound has released the slot.",
		investigationTargetLabel(record.Tenant, record.Environment), investigationAge(investigationLifetime), record.ReportPath))
}

// orchestratorSessionPID is the process behind a running orchestrator session,
// or 0 when nothing is running for that id.
func (a *App) orchestratorSessionPID(id string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	managed := a.sessions[orchestratorSessionKey(id)]
	if managed == nil || managed.closed || managed.session == nil {
		return 0
	}
	return managed.session.Pid()
}

// investigationRefusalReason names the bound that refused, for the log line.
func investigationRefusalReason(err error) string {
	var refusal *investigationRefusal
	if errors.As(err, &refusal) {
		return refusal.reason
	}
	return "error"
}

// investigationRefusalDetails reports the reason, the operator-facing message,
// and the already-admitted investigation this refusal points at (empty for a
// hard refusal such as a thin report or a full population), so a caller can
// offer "focus that one" instead of only surfacing the refusal as an error.
func investigationRefusalDetails(err error) (reason, message, existingID string) {
	var refusal *investigationRefusal
	if errors.As(err, &refusal) {
		return refusal.reason, refusal.message, refusal.existingID
	}
	return "error", err.Error(), ""
}

// investigateWorkingDir resolves the env's host workspace to run the
// investigation in; "" (no synced workspace) falls back to the harness default.
func (a *App) investigateWorkingDir(tenant, environment string) string {
	if strings.TrimSpace(tenant) == "" || strings.TrimSpace(environment) == "" {
		return ""
	}
	_, path, err := a.resolveHostWorkspace(uiSelection{Tenant: tenant, Environment: environment})
	if err != nil {
		return ""
	}
	return path
}

// stageReport writes the report under the registry's report directory and
// returns its path for the seed prompt to reference. Every report is staged,
// including one no investigation will read: a refused report is the record that
// it was reported, and the staged file is what an operator opens to see why.
func (r *investigationRegistry) stageReport(report string) (string, error) {
	dir := r.reportDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create investigation dir: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("report-%d.md", time.Now().UnixNano()))
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		return "", fmt.Errorf("write investigation report: %w", err)
	}
	pruneInvestigationReports(dir, investigationReportRetention)
	return path, nil
}

// writeInvestigationLog opens the investigation's own log, which the attached
// job serves as its output. It records what was admitted and under which bounds,
// so the job surface answers "what is this agent, and when will it stop" without
// anyone reading desktop logs.
func (r *investigationRegistry) writeInvestigationLog(record investigationRecord) string {
	path := filepath.Join(r.reportDir, record.ID+".log")
	header := strings.Join([]string{
		fmt.Sprintf("investigation %s for %s", record.ID, investigationTargetLabel(record.Tenant, record.Environment)),
		"report: " + record.ReportPath,
		"signature: " + record.Signature,
		"started: " + record.StartedAt.UTC().Format(time.RFC3339),
		fmt.Sprintf("lifetime bound: %s — an investigation still running at the bound is stopped", investigationAge(investigationLifetime)),
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(header), 0o644); err != nil {
		log.Printf("erun-app: write investigation log %s: %v", path, err)
		return ""
	}
	return path
}

func (r *investigationRegistry) appendInvestigationLog(record investigationRecord, line string) {
	if strings.TrimSpace(record.LogPath) == "" {
		return
	}
	file, err := os.OpenFile(record.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("erun-app: append investigation log %s: %v", record.LogPath, err)
		return
	}
	defer func() { _ = file.Close() }()
	if _, err := fmt.Fprintln(file, line); err != nil {
		log.Printf("erun-app: append investigation log %s: %v", record.LogPath, err)
	}
}

// pruneInvestigationReports keeps the newest staged reports. They are evidence,
// so they outlive the investigation that read them — but only up to a bound: an
// investigate directory that only ever grows is how a handful of real failures
// came to look like dozens.
func pruneInvestigationReports(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	reports := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "report-") {
			continue
		}
		reports = append(reports, entry.Name())
	}
	if len(reports) <= keep {
		return
	}
	// The names carry a nanosecond stamp of equal width, so lexical order is
	// chronological order.
	sort.Sort(sort.Reverse(sort.StringSlice(reports)))
	for _, name := range reports[keep:] {
		_ = os.Remove(filepath.Join(dir, name))
	}
}
