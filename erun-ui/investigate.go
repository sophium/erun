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

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// InvestigateFailure stages a failure report to a host file and, within the
// investigation bounds, spawns a transient orchestrator to investigate it. A
// refusal is returned as an error naming the bound.
func (a *App) InvestigateFailure(report, tenant, environment string, cols, rows int) (orchestratorInfo, error) {
	name := "Investigate"
	if env := strings.TrimSpace(environment); env != "" {
		name = "Investigate " + env
	}
	return a.spawnFailureAgent("investigate", report, tenant, environment, cols, rows, name, func(path string) string {
		return fmt.Sprintf("A failure report is saved at %s. Read it, then plan to fix the issue, or improve error reporting so similar issues can be fixed faster. If this is an erun platform issue rather than a project issue, file a bug in the erun GitHub tracker (sophium/erun) using the erun-file-issue skill.", path)
	})
}

// spawnFailureAgent is the bounded-spawn plumbing shared by investigations and
// bug-report drafts.
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
		// The refused report stays staged as the record of what was reported.
		log.Printf("erun-app: %s refused for %s (%s): report=%s", idPrefix, investigationTargetLabel(tenant, environment), investigationRefusalReason(err), path)
		return orchestratorInfo{}, err
	}

	var envs []config.OrchestratorEnvConfig
	if t, env := strings.TrimSpace(tenant), strings.TrimSpace(environment); t != "" && env != "" {
		envs = []config.OrchestratorEnvConfig{
			{Tenant: t, Environment: env, Directory: a.investigateWorkingDir(t, env)},
		}
	}
	info, err := a.spawnOrchestratorSession(orchestratorSpawn{
		id:            id,
		name:          name,
		launch:        defaultOrchestratorLaunchPlan(),
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

func (a *App) activateInvestigation(record investigationRecord, name string) {
	record.LogPath = a.investigations.writeInvestigationLog(record)
	record.LeaseID = a.attachInvestigationJob(record, name)
	a.investigations.activate(record.ID, record.ReportPath, record.LogPath, record.LeaseID)
}

// The job's lease makes the environment report busy while the agent runs.
// Failure is logged, not fatal: the investigation is already running.
func (a *App) attachInvestigationJob(record investigationRecord, name string) string {
	if record.Tenant == "" || record.Environment == "" {
		return ""
	}
	pid := a.orchestratorSessionPID(record.ID)
	if pid <= 0 {
		log.Printf("erun-app: investigation %s has no pid to track; it will not appear as a job", record.ID)
		return ""
	}
	job, err := jobs.AttachEnvironmentJob(core.Context{}, model.AttachEnvironmentJobParams{
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

// Releases the lease so the environment stops reading as busy.
func (a *App) finishExpiredInvestigation(record investigationRecord) {
	a.stopOrchestratorSession(record.ID)
	a.investigations.appendInvestigationLog(record, fmt.Sprintf("terminated: still running after the %s lifetime bound without concluding; stopped by the desktop", investigationAge(investigationLifetime)))
	if record.Tenant != "" && record.Environment != "" && record.LeaseID != "" {
		if _, err := activity.ReleaseEnvironmentActivityLease(record.Tenant, record.Environment, record.LeaseID); err != nil {
			log.Printf("erun-app: release investigation lease %s: %v", record.LeaseID, err)
		}
	}
	log.Printf("erun-app: investigation %s stopped after the %s lifetime bound", record.ID, investigationAge(investigationLifetime))
	a.emitAppNotification("warning", fmt.Sprintf("The investigation of %s was stopped after %s without concluding. Its report is at %s — read it, or start a fresh investigation now that the bound has released the slot.",
		investigationTargetLabel(record.Tenant, record.Environment), investigationAge(investigationLifetime), record.ReportPath))
}

func (a *App) orchestratorSessionPID(id string) int {
	pid := 0
	a.terminal.WithLock(func(l *terminal.Locked) {
		managed := l.Session(terminal.OrchestratorSessionKey(id))
		if managed == nil || managed.Closed || managed.PTY == nil {
			return
		}
		pid = managed.PTY.Pid()
	})
	return pid
}

func investigationRefusalReason(err error) string {
	var refusal *investigationRefusalError
	if errors.As(err, &refusal) {
		return refusal.reason
	}
	return "error"
}

// existingID is empty for a hard refusal (thin report, full population).
func investigationRefusalDetails(err error) (reason, message, existingID string) {
	var refusal *investigationRefusalError
	if errors.As(err, &refusal) {
		return refusal.reason, refusal.message, refusal.existingID
	}
	return "error", err.Error(), ""
}

// "" falls back to the harness default.
func (a *App) investigateWorkingDir(tenant, environment string) string {
	if strings.TrimSpace(tenant) == "" || strings.TrimSpace(environment) == "" {
		return ""
	}
	_, path, err := a.envops.ResolveHostWorkspace(uimodel.Selection{Tenant: tenant, Environment: environment})
	if err != nil {
		return ""
	}
	return path
}

// Every report is staged, including refused ones.
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

// Reports outlive their investigation, but only up to a bound.
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
	// Equal-width nanosecond stamps make lexical order chronological.
	sort.Sort(sort.Reverse(sort.StringSlice(reports)))
	for _, name := range reports[keep:] {
		_ = os.Remove(filepath.Join(dir, name))
	}
}
