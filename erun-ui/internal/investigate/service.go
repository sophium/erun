package investigate

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
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Deps reaches the orchestrator sessions a failure agent runs in. Each func may
// take the terminal lock, and the registry calls Running under its own lock, so
// the service must never be called while the terminal lock is held.
type Deps struct {
	// Running reports whether id still has a live session on this desktop.
	Running func(id string) bool
	// StopSession stops id's session; an expired investigation is stopped this way.
	StopSession func(id string)
	// SessionPID is id's session process, or 0 when it has none to track.
	SessionPID func(id string) int
	// HostWorkspace is the selection's host worktree, the agent's working directory.
	HostWorkspace func(uimodel.Selection) (string, error)
	Emit          uimodel.Emitter

	// ReportDir holds the staged reports and the investigation logs.
	ReportDir string
	// Now and After default to the wall clock; tests inject both so none sleeps.
	Now   func() time.Time
	After func(time.Duration, func()) Timer
}

type Service struct {
	d        Deps
	registry *investigationRegistry
}

// New panics on a missing seam rather than defaulting it, so a test can never
// reach a session it did not wire.
func New(d Deps) *Service {
	if missing := missingSeams(d); len(missing) > 0 {
		panic("investigate.New: missing " + strings.Join(missing, ", "))
	}
	s := &Service{d: d, registry: newInvestigationRegistry(d.ReportDir)}
	if d.Now != nil {
		s.registry.now = d.Now
	}
	if d.After != nil {
		s.registry.after = d.After
	}
	s.registry.live = d.Running
	s.registry.onExpire = s.finishExpiredInvestigation
	return s
}

func missingSeams(d Deps) []string {
	required := []struct {
		name    string
		missing bool
	}{
		{"Running", d.Running == nil},
		{"StopSession", d.StopSession == nil},
		{"SessionPID", d.SessionPID == nil},
		{"HostWorkspace", d.HostWorkspace == nil},
		{"Emit", d.Emit == nil},
		{"ReportDir", strings.TrimSpace(d.ReportDir) == ""},
	}
	var missing []string
	for _, seam := range required {
		if seam.missing {
			missing = append(missing, seam.name)
		}
	}
	return missing
}

// StopTimers drops every pending lifetime stop; shutdown calls it.
func (s *Service) StopTimers() {
	s.registry.stopTimers()
}

// Request is one failure-agent spawn: the report it works from and how its
// session is named and seeded.
type Request struct {
	IDPrefix    string
	Report      string
	Tenant      string
	Environment string
	Name        string
	Cols        int
	Rows        int
	Prompt      func(reportPath string) string
}

// Transient is the transient orchestrator session a spawn asks its caller to
// start; its fields mirror the orchestrator service's TransientSpawn.
type Transient struct {
	ID            string
	Name          string
	Envs          []config.OrchestratorEnvConfig
	InitialPrompt string
	Cols          int
	Rows          int
}

// InvestigateRequest stages a failure report for a transient orchestrator to investigate.
func InvestigateRequest(report, tenant, environment string, cols, rows int) Request {
	name := "Investigate"
	if env := strings.TrimSpace(environment); env != "" {
		name = "Investigate " + env
	}
	return Request{
		IDPrefix:    "investigate",
		Report:      report,
		Tenant:      tenant,
		Environment: environment,
		Name:        name,
		Cols:        cols,
		Rows:        rows,
		Prompt: func(path string) string {
			return fmt.Sprintf("A failure report is saved at %s. Read it, then plan to fix the issue, or improve error reporting so similar issues can be fixed faster. If this is an erun platform issue rather than a project issue, file a bug in the erun GitHub tracker (sophium/erun) using the erun-file-issue skill.", path)
		},
	}
}

// Spawn stages the report and, within the investigation bounds, has start run
// the agent's session. A refusal is returned as an error naming the bound.
func (s *Service) Spawn(request Request, start func(Transient) error) error {
	report := strings.TrimSpace(request.Report)
	if report == "" {
		return fmt.Errorf("nothing to work from: the failure report is empty")
	}
	tenant, environment := request.Tenant, request.Environment
	path, err := s.registry.stageReport(report)
	if err != nil {
		return err
	}
	id := fmt.Sprintf("%s-%d", request.IDPrefix, time.Now().UnixNano())
	if err := s.registry.admit(id, report, tenant, environment); err != nil {
		// The refused report stays staged as the record of what was reported.
		log.Printf("erun-app: %s refused for %s (%s): report=%s", request.IDPrefix, investigationTargetLabel(tenant, environment), investigationRefusalReason(err), path)
		return err
	}

	var envs []config.OrchestratorEnvConfig
	if t, env := strings.TrimSpace(tenant), strings.TrimSpace(environment); t != "" && env != "" {
		envs = []config.OrchestratorEnvConfig{
			{Tenant: t, Environment: env, Directory: s.investigateWorkingDir(t, env)},
		}
	}
	if err := start(Transient{
		ID:            id,
		Name:          request.Name,
		Envs:          envs,
		InitialPrompt: request.Prompt(path),
		Cols:          request.Cols,
		Rows:          request.Rows,
	}); err != nil {
		s.registry.discard(id)
		return err
	}
	s.activateInvestigation(investigationRecord{
		ID:          id,
		Tenant:      strings.TrimSpace(tenant),
		Environment: strings.TrimSpace(environment),
		ReportPath:  path,
		Signature:   investigationSignature(report, tenant, environment),
		StartedAt:   s.registry.startedAt(id),
	}, request.Name)
	return nil
}

func (s *Service) activateInvestigation(record investigationRecord, name string) {
	record.LogPath = s.registry.writeInvestigationLog(record)
	record.LeaseID = s.attachInvestigationJob(record, name)
	s.registry.activate(record.ID, record.ReportPath, record.LogPath, record.LeaseID)
}

// The job's lease makes the environment report busy while the agent runs.
// Failure is logged, not fatal: the investigation is already running.
func (s *Service) attachInvestigationJob(record investigationRecord, name string) string {
	if record.Tenant == "" || record.Environment == "" {
		return ""
	}
	pid := s.d.SessionPID(record.ID)
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
func (s *Service) finishExpiredInvestigation(record investigationRecord) {
	s.d.StopSession(record.ID)
	s.registry.appendInvestigationLog(record, fmt.Sprintf("terminated: still running after the %s lifetime bound without concluding; stopped by the desktop", investigationAge(investigationLifetime)))
	if record.Tenant != "" && record.Environment != "" && record.LeaseID != "" {
		if _, err := activity.ReleaseEnvironmentActivityLease(record.Tenant, record.Environment, record.LeaseID); err != nil {
			log.Printf("erun-app: release investigation lease %s: %v", record.LeaseID, err)
		}
	}
	log.Printf("erun-app: investigation %s stopped after the %s lifetime bound", record.ID, investigationAge(investigationLifetime))
	s.d.Emit(uimodel.AppNotificationEvent, uimodel.AppNotificationPayload{Kind: "warning", Message: fmt.Sprintf("The investigation of %s was stopped after %s without concluding. Its report is at %s — read it, or start a fresh investigation now that the bound has released the slot.",
		investigationTargetLabel(record.Tenant, record.Environment), investigationAge(investigationLifetime), record.ReportPath)})
}

func investigationRefusalReason(err error) string {
	var refusal *investigationRefusalError
	if errors.As(err, &refusal) {
		return refusal.reason
	}
	return "error"
}

// RefusalDetails splits a Spawn error into its bound's reason and message;
// existingID is empty for a hard refusal (thin report, full population).
func RefusalDetails(err error) (reason, message, existingID string) {
	var refusal *investigationRefusalError
	if errors.As(err, &refusal) {
		return refusal.reason, refusal.message, refusal.existingID
	}
	return "error", err.Error(), ""
}

// "" falls back to the harness default.
func (s *Service) investigateWorkingDir(tenant, environment string) string {
	if strings.TrimSpace(tenant) == "" || strings.TrimSpace(environment) == "" {
		return ""
	}
	path, err := s.d.HostWorkspace(uimodel.Selection{Tenant: tenant, Environment: environment})
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
