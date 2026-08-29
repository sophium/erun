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

// An investigation is a full AI agent on an account shared with every
// orchestrator driving these environments, so the question "should this report
// spawn one" is a resource decision, not a formality. Left unbounded it answers
// yes to every report: duplicate reports from one failure, the same failure
// recurring for a day, and reports carrying no evidence at all each got an agent
// of their own, none of them bounded in time. The population then sat on the
// account's agent limit until it reset, and while it did, no orchestrator here
// could delegate anything.
//
// The five bounds below are that decision. They are deliberately conservative:
// refusing an investigation costs a report an operator can still act on
// manually, while admitting one too many costs every other agent in the account.

const (
	// investigationEventWindow folds the reports of one failure into one
	// investigation. A failing operation emits its reports as it unwinds —
	// observed pairs are milliseconds apart — so anything arriving for the same
	// environment inside this window is the same event, whatever it says.
	investigationEventWindow = time.Minute

	// investigationCooldown spaces investigations of the same failure. Observed
	// bursts repeated at 55 minutes and at a few hours, each burst adding agents
	// that outlived it; two hours covers the sub-hourly repeat while still
	// letting a failure that survives the day be looked at again.
	investigationCooldown = 2 * time.Hour

	// maxLiveInvestigations caps the population. Four concurrent agents is what
	// exhausted the account, and an investigation is the least important thing
	// running on it — the operator's own orchestrators are what the remaining
	// quota is for. Two lets an unrelated second environment still be picked up
	// without the count ever growing.
	maxLiveInvestigations = 2

	// investigationLifetime bounds one investigation. Reading a report,
	// reproducing it, and either fixing it or filing it is minutes of work; the
	// runaways sat at 21 hours and at nearly seven days with nothing to show. An
	// investigation still running at this point is not converging, and holding
	// quota is the only thing it is reliably doing.
	investigationLifetime = 30 * time.Minute

	// investigationHistoryCap bounds what the registry remembers. Cooldowns need
	// recent history, not all of it.
	investigationHistoryCap = 50

	// investigationReportRetention bounds the staged reports on disk. They are
	// evidence, so they outlive the investigation that read them, but a directory
	// that only grows is how 76 of them accumulated unnoticed.
	investigationReportRetention = 50
)

// investigationState is what an investigation is doing now, as the registry can
// observe it: its session is alive, it ended on its own, or the lifetime bound
// stopped it.
type investigationState string

const (
	// investigationPending is an admitted slot whose session has not started
	// yet. It counts against the cap from the moment it is admitted: events
	// arrive together, and a cap decided before the previous spawn has landed is
	// not a cap at all.
	investigationPending investigationState = "pending"
	investigationRunning investigationState = "running"
	investigationEnded   investigationState = "ended"
	investigationExpired investigationState = "expired"
)

// investigationRecord is one admitted investigation. Suppressed counts the
// reports folded into it, so a burst reads as one investigation that absorbed
// several reports rather than as reports that vanished.
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

// investigationRefusal is a decision not to spawn, carrying the operator-facing
// reason. It is returned as an error because refusing is the answer to the
// operator's action, and the desktop already renders that answer beside the
// orchestrator list.
type investigationRefusal struct {
	reason  string
	message string
	// existingID names the already-admitted investigation this refusal points
	// at (the same-event and already-investigating reasons only), so a caller
	// can focus that one instead of only reporting the refusal as an error.
	existingID string
}

func (r *investigationRefusal) Error() string { return r.message }

// investigationTimer is the lifetime bound's handle, so a session that ends on
// its own does not leave a timer waiting to stop something already gone.
type investigationTimer interface{ Stop() bool }

// investigationRegistry decides which reports become investigations and holds
// the live population. Liveness is re-observed from the session registry rather
// than tracked here: a session that exited must free its slot without depending
// on an exit hook firing.
type investigationRegistry struct {
	mu        sync.Mutex
	records   []*investigationRecord
	timers    map[string]investigationTimer
	reportDir string
	now       func() time.Time
	after     func(time.Duration, func()) investigationTimer
	// live answers whether an admitted investigation's session is still running.
	live func(id string) bool
	// onExpire runs when the lifetime bound elapses with the session still alive.
	onExpire func(investigationRecord)
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

// defaultInvestigationReportDir is where failure reports are staged for the
// agent to read. It is a field on the registry rather than a constant so a test
// never writes into the developer's shared temp dir — every run of the desktop
// suite used to leave two reports there, which is most of how the directory grew
// to 76 files that read as 76 spawned agents.
func defaultInvestigationReportDir() string {
	return filepath.Join(os.TempDir(), "erun-investigate")
}

// admit is the whole spawn decision. On success it reserves the slot for id
// under the same lock the decision was taken under — a decision that released
// the lock before recording would let concurrent events all pass a cap that none
// of them had filled yet — and the caller must then either activate(id) it or
// discard(id) it. On refusal it returns an investigationRefusal naming the bound
// that stopped the spawn.
func (r *investigationRegistry) admit(id, report, tenant, environment string) error {
	if !investigationReportHasDiagnosticContent(report) {
		return &investigationRefusal{
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
			return &investigationRefusal{
				reason: "same-event",
				message: fmt.Sprintf("%s is already being investigated as %s, started %s ago. This report is part of the same failure event, so it was recorded against that investigation instead of starting another.",
					investigationTargetLabel(tenant, environment), existing.ID, investigationAge(now.Sub(existing.StartedAt))),
				existingID: existing.ID,
			}
		}
		if existing.Signature == signature {
			existing.Suppressed++
			return &investigationRefusal{
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
			return &investigationRefusal{
				reason: "cooldown",
				message: fmt.Sprintf("That failure was investigated %s ago as %s (%s). Investigations of the same failure are spaced %s apart so a failure that keeps repeating cannot accumulate agents; the next one for it can start in %s.",
					investigationAge(since), previous.ID, previous.State, investigationAge(investigationCooldown), investigationAge(investigationCooldown-since)),
			}
		}
	}
	if len(live) >= maxLiveInvestigations {
		return &investigationRefusal{
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

// startedAt is when the registry admitted this investigation, which is the
// instant every bound is measured from.
func (r *investigationRegistry) startedAt(id string) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry := r.findLocked(id); entry != nil {
		return entry.StartedAt
	}
	return r.now()
}

// activate promotes a reserved slot to a running investigation, records where
// its report and log live, and arms the lifetime bound.
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

// discard releases a reserved slot whose session never started, so a failed
// spawn does not hold capacity or start a cooldown for a failure nobody looked at.
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

// expire enforces the lifetime bound: an investigation still alive at the bound
// is stopped and says so. One that already ended is left exactly as it ended.
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

// list returns the population as it stands, newest first, reconciled against
// live sessions so a record can never claim to be running after its session is
// gone.
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

// stopTimers releases the lifetime timers without touching the sessions, for a
// desktop shutdown that is closing them anyway.
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

// reconcileLocked demotes records whose session is gone, so the cap and the
// dedupe both count what is actually running.
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

// liveLocked is everything holding a slot: sessions that are running, plus the
// slots reserved for sessions that are starting.
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

// pruneLocked keeps the newest records. Running ones are never dropped: the cap
// is decided from them.
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
	r.records = append(kept, finished...)
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

// investigationAge renders a duration the way the refusal reads aloud, without
// the sub-second noise time.Duration prints.
func investigationAge(d time.Duration) string {
	if d < time.Second {
		return "less than a second"
	}
	return d.Round(time.Second).String()
}

// investigationSignature identifies the failure a report is about, so the same
// failure recurring hours later is recognised as the same failure rather than as
// a new one. Only the lines that name the failure take part, with digits masked:
// versions, elapsed times, and timestamps differ on every occurrence of one
// failure, and including them would make every occurrence look new.
func investigationSignature(report, tenant, environment string) string {
	lines := investigationIdentityLines(report)
	seed := strings.ToLower(investigationEventKey(tenant, environment)) + "\n" + strings.Join(lines, "\n")
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:8])
}

var investigationDigits = regexp.MustCompile(`\d+`)

// investigationIdentityLabels are the labelled lines that say which failure this
// is. Version, namespace, timing, and output lines are deliberately absent: they
// move between occurrences of the same failure.
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

// The floor on the input. A report is worth an agent when it carries something
// the agent can act on: the command that failed, the status it failed with, a
// named failure state, or captured output. A report with none of those cannot be
// investigated — the agent either invents a hypothesis or thrashes — and the
// missing evidence is the bug, in the reporting rather than in the system that
// failed.
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

// investigationHasLabelledDetail accepts a labelled diagnostic section, whether
// its content sits on the label's own line ("Error: timed out") or on the lines
// under it ("Output:" followed by a log tail).
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
