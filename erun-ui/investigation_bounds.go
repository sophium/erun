package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Investigations share the account's agent limit with every orchestrator, so
// the bounds below are deliberately conservative.

const (
	// investigationEventWindow folds one failure's unwinding reports, which arrive
	// milliseconds apart, into one investigation.
	investigationEventWindow = time.Minute

	// investigationCooldown covers observed sub-hourly repeats of one failure.
	investigationCooldown = 2 * time.Hour

	// maxLiveInvestigations leaves the quota to the operator's own orchestrators.
	maxLiveInvestigations = 2

	// investigationLifetime far exceeds the minutes a useful investigation takes;
	// one still running past it is not converging.
	investigationLifetime = 30 * time.Minute

	investigationHistoryCap = 50

	investigationReportRetention = 50
)

type investigationState string

const (
	// investigationPending counts against the cap from admission, since events
	// arrive together.
	investigationPending investigationState = "pending"
	investigationRunning investigationState = "running"
	investigationEnded   investigationState = "ended"
	investigationExpired investigationState = "expired"
)

// Suppressed counts the reports folded into this one.
type investigationRecord struct {
	ID          string
	Signature   string
	EventKey    string
	Tenant      string
	Environment string
	ReportPath  string
	LogPath     string
	LeaseID     string
	StartedAt   time.Time
	EndedAt     time.Time
	State       investigationState
	Suppressed  int
}

type investigationRefusalError struct {
	reason  string
	message string
	// existingID is set for the same-event and already-investigating reasons.
	existingID string
}

func (r *investigationRefusalError) Error() string { return r.message }

type investigationTimer interface{ Stop() bool }

// Liveness is re-observed from the session registry so an exited session frees
// its slot without an exit hook.
type investigationRegistry struct {
	mu        sync.Mutex
	records   []*investigationRecord
	timers    map[string]investigationTimer
	reportDir string
	now       func() time.Time
	after     func(time.Duration, func()) investigationTimer
	live      func(id string) bool
	onExpire  func(investigationRecord)
}

func newInvestigationRegistry(reportDir string) *investigationRegistry {
	return &investigationRegistry{
		timers:    map[string]investigationTimer{},
		reportDir: reportDir,
		now:       time.Now,
		after: func(d time.Duration, f func()) investigationTimer {
			return time.AfterFunc(d, f)
		},
		live: func(string) bool { return true },
	}
}

// A registry field rather than a constant so tests never write to the shared
// temp dir.
func defaultInvestigationReportDir() string {
	return filepath.Join(os.TempDir(), "erun-investigate")
}

// admit reserves the slot under the decision's lock so concurrent events cannot
// all pass the cap. The caller must then activate or discard id.
func (r *investigationRegistry) admit(id, report, tenant, environment string) error {
	if !investigationReportHasDiagnosticContent(report) {
		return &investigationRefusalError{
			reason: "thin-report",
			message: "This failure report carries no diagnostic content — no command, no exit status, and no captured output — so an investigation would have nothing to work from. " +
				"The gap is in the failure reporting: capture the command, its exit status, and the output tail, then investigate again.",
		}
	}
	signature := investigationSignature(report, tenant, environment)
	eventKey := investigationEventKey(tenant, environment)

	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.reconcileLocked(now)

	live := r.liveLocked()
	for _, existing := range live {
		if existing.EventKey == eventKey && now.Sub(existing.StartedAt) < investigationEventWindow {
			existing.Suppressed++
			return &investigationRefusalError{
				reason: "same-event",
				message: fmt.Sprintf("%s is already being investigated as %s, started %s ago. This report is part of the same failure event, so it was recorded against that investigation instead of starting another.",
					investigationTargetLabel(tenant, environment), existing.ID, investigationAge(now.Sub(existing.StartedAt))),
				existingID: existing.ID,
			}
		}
		if existing.Signature == signature {
			existing.Suppressed++
			return &investigationRefusalError{
				reason: "already-investigating",
				message: fmt.Sprintf("That failure is already under investigation as %s, started %s ago. Watch or stop that investigation rather than starting a second one for the same failure.",
					existing.ID, investigationAge(now.Sub(existing.StartedAt))),
				existingID: existing.ID,
			}
		}
	}
	if previous, ok := r.lastForSignatureLocked(signature); ok {
		if since := now.Sub(previous.StartedAt); since < investigationCooldown {
			previous.Suppressed++
			return &investigationRefusalError{
				reason: "cooldown",
				message: fmt.Sprintf("That failure was investigated %s ago as %s (%s). Investigations of the same failure are spaced %s apart so a failure that keeps repeating cannot accumulate agents; the next one for it can start in %s.",
					investigationAge(since), previous.ID, previous.State, investigationAge(investigationCooldown), investigationAge(investigationCooldown-since)),
			}
		}
	}
	if len(live) >= maxLiveInvestigations {
		return &investigationRefusalError{
			reason: "at-capacity",
			message: fmt.Sprintf("%d investigations are already running (%s), which is the limit. They share the agent account with every orchestrator driving these environments, so stop one before starting another.",
				len(live), strings.Join(investigationIDs(live), ", ")),
		}
	}
	r.records = append(r.records, &investigationRecord{
		ID:          id,
		Signature:   signature,
		EventKey:    eventKey,
		Tenant:      strings.TrimSpace(tenant),
		Environment: strings.TrimSpace(environment),
		StartedAt:   now,
		State:       investigationPending,
	})
	r.pruneLocked()
	return nil
}

// Every bound is measured from admission.
func (r *investigationRegistry) startedAt(id string) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry := r.findLocked(id); entry != nil {
		return entry.StartedAt
	}
	return r.now()
}

func (r *investigationRegistry) activate(id, reportPath, logPath, leaseID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.findLocked(id)
	if entry == nil {
		return
	}
	entry.State = investigationRunning
	entry.ReportPath = reportPath
	entry.LogPath = logPath
	entry.LeaseID = leaseID
	if r.after == nil {
		return
	}
	r.timers[id] = r.after(investigationLifetime, func() { r.expire(id) })
}

// A failed spawn holds no capacity and starts no cooldown.
func (r *investigationRegistry) discard(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for index, entry := range r.records {
		if entry.ID == id {
			r.records = append(r.records[:index], r.records[index+1:]...)
			return
		}
	}
}

// One that already ended is left exactly as it ended.
func (r *investigationRegistry) expire(id string) {
	r.mu.Lock()
	delete(r.timers, id)
	entry := r.findLocked(id)
	if entry == nil || entry.State != investigationRunning {
		r.mu.Unlock()
		return
	}
	if r.live != nil && !r.live(id) {
		entry.State = investigationEnded
		entry.EndedAt = r.now()
		r.mu.Unlock()
		return
	}
	entry.State = investigationExpired
	entry.EndedAt = r.now()
	snapshot := *entry
	onExpire := r.onExpire
	r.mu.Unlock()
	if onExpire != nil {
		onExpire(snapshot)
	}
}

func (r *investigationRegistry) list() []investigationRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reconcileLocked(r.now())
	out := make([]investigationRecord, 0, len(r.records))
	for _, entry := range r.records {
		out = append(out, *entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

func (r *investigationRegistry) stopTimers() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, timer := range r.timers {
		if timer != nil {
			timer.Stop()
		}
		delete(r.timers, id)
	}
}

func (r *investigationRegistry) reconcileLocked(now time.Time) {
	for _, entry := range r.records {
		if entry.State != investigationRunning {
			continue
		}
		if r.live != nil && !r.live(entry.ID) {
			entry.State = investigationEnded
			entry.EndedAt = now
			if timer := r.timers[entry.ID]; timer != nil {
				timer.Stop()
				delete(r.timers, entry.ID)
			}
		}
	}
}

// Includes slots reserved for sessions still starting.
func (r *investigationRegistry) liveLocked() []*investigationRecord {
	out := make([]*investigationRecord, 0, len(r.records))
	for _, entry := range r.records {
		if entry.State == investigationRunning || entry.State == investigationPending {
			out = append(out, entry)
		}
	}
	return out
}

func (r *investigationRegistry) lastForSignatureLocked(signature string) (*investigationRecord, bool) {
	var newest *investigationRecord
	for _, entry := range r.records {
		if entry.Signature != signature {
			continue
		}
		if newest == nil || entry.StartedAt.After(newest.StartedAt) {
			newest = entry
		}
	}
	return newest, newest != nil
}

func (r *investigationRegistry) findLocked(id string) *investigationRecord {
	for _, entry := range r.records {
		if entry.ID == id {
			return entry
		}
	}
	return nil
}

// Running records are never dropped: the cap is decided from them.
func (r *investigationRegistry) pruneLocked() {
	if len(r.records) <= investigationHistoryCap {
		return
	}
	kept := make([]*investigationRecord, 0, len(r.records))
	finished := make([]*investigationRecord, 0, len(r.records))
	for _, entry := range r.records {
		if entry.State == investigationRunning || entry.State == investigationPending {
			kept = append(kept, entry)
			continue
		}
		finished = append(finished, entry)
	}
	sort.Slice(finished, func(i, j int) bool { return finished[i].StartedAt.After(finished[j].StartedAt) })
	room := investigationHistoryCap - len(kept)
	if room < 0 {
		room = 0
	}
	if len(finished) > room {
		finished = finished[:room]
	}
	kept = append(kept, finished...)
	r.records = kept
	sort.Slice(r.records, func(i, j int) bool { return r.records[i].StartedAt.Before(r.records[j].StartedAt) })
}

func investigationIDs(records []*investigationRecord) []string {
	out := make([]string, 0, len(records))
	for _, entry := range records {
		out = append(out, entry.ID)
	}
	sort.Strings(out)
	return out
}

func investigationEventKey(tenant, environment string) string {
	return strings.TrimSpace(tenant) + "/" + strings.TrimSpace(environment)
}

func investigationTargetLabel(tenant, environment string) string {
	if key := investigationEventKey(tenant, environment); key != "/" {
		return key
	}
	return "This environment"
}

func investigationAge(d time.Duration) string {
	if d < time.Second {
		return "less than a second"
	}
	return d.Round(time.Second).String()
}

// Only failure-naming lines count, digits masked, so recurrences of one failure
// share a signature.
func investigationSignature(report, tenant, environment string) string {
	lines := investigationIdentityLines(report)
	seed := strings.ToLower(investigationEventKey(tenant, environment)) + "\n" + strings.Join(lines, "\n")
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:8])
}

var investigationDigits = regexp.MustCompile(`\d+`)

var investigationIdentityLabels = []string{"target:", "error:", "reason:", "message:", "command:"}

func investigationIdentityLines(report string) []string {
	out := make([]string, 0, len(investigationIdentityLabels)+1)
	for _, raw := range strings.Split(report, "\n") {
		line := strings.ToLower(strings.TrimSpace(raw))
		if line == "" {
			continue
		}
		keep := len(out) == 0
		for _, label := range investigationIdentityLabels {
			if strings.HasPrefix(line, label) {
				keep = true
				break
			}
		}
		if !keep {
			continue
		}
		out = append(out, investigationDigits.ReplaceAllString(line, "#"))
	}
	return out
}

// A report is worth an agent only with actionable evidence: the failed command,
// a status, a named failure state, or captured output.
var (
	investigationCommandLine  = regexp.MustCompile(`(?im)^[\s>]*(?:\$\s+|(?:command|cmd)\s*:\s*)?(?:erun|helm|kubectl|docker|git|go|make|npm|yarn|pnpm|terraform|aws|python3?|node|sh|bash|pwsh|powershell)\s+\S`)
	investigationExitStatus   = regexp.MustCompile(`(?i)\b(?:exit(?:ed)?\s+(?:status|code)|exitcode|exit_code|signal)\b\s*[:=]?\s*-?\d+`)
	investigationFailureState = regexp.MustCompile(`(?i)(oomkilled|crashloopbackoff|imagepullbackoff|errimagepull|createcontainerconfigerror|segmentation fault|panic:|traceback \(most recent call last\))`)
)

var investigationDetailLabels = []string{"output:", "logs:", "log:", "stderr:", "stdout:", "error:", "reason:", "message:", "containers:"}

func investigationReportHasDiagnosticContent(report string) bool {
	text := strings.TrimSpace(report)
	if text == "" {
		return false
	}
	if investigationCommandLine.MatchString(text) ||
		investigationExitStatus.MatchString(text) ||
		investigationFailureState.MatchString(text) {
		return true
	}
	return investigationHasLabelledDetail(text)
}

// The detail may sit on the label's line or on the lines under it.
func investigationHasLabelledDetail(text string) bool {
	lines := strings.Split(text, "\n")
	for index, raw := range lines {
		line := strings.ToLower(strings.TrimSpace(raw))
		for _, label := range investigationDetailLabels {
			if !strings.HasPrefix(line, label) {
				continue
			}
			if strings.TrimSpace(line[len(label):]) != "" {
				return true
			}
			if investigationHasNonEmptyLine(lines[index+1:]) {
				return true
			}
		}
	}
	return false
}

func investigationHasNonEmptyLine(lines []string) bool {
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			return true
		}
	}
	return false
}
