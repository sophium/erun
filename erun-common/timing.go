package eruncommon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// stepTiming is one measured step in a long command's run, arranged as a
// tree so a phase's own duration and its children's durations can both be
// read: build → image → per-architecture build/promote, or release → stage.
// A command's whole run is the root; RunBuildExecution/RunReleaseExecution/
// RunPushCommand/RunDeploySpecs each start one and report it on completion.
type stepTiming struct {
	mu       sync.Mutex
	name     string
	now      func() time.Time
	start    time.Time
	end      time.Time
	ended    bool
	failed   bool
	errMsg   string
	cache    *cacheDecision
	children []*stepTiming
	// cgroupBefore is sampled at step creation so finish() can diff against it;
	// cgroupMetrics is that diff, computed once the step ends. See
	// build_cgroup_metrics.go.
	cgroupBefore  buildCgroupSnapshot
	cgroupMetrics *BuildCgroupMetrics
}

// cacheDecision records whether an image build promoted from the fingerprint
// cache or rebuilt, and why, so the timing report can answer "how long" and
// "why" from the same row instead of sending a reader back to the trace.
type cacheDecision struct {
	hit        bool
	missReason string
}

func newStepTiming(name string, now func() time.Time) *stepTiming {
	if now == nil {
		now = time.Now
	}
	return &stepTiming{name: name, now: now, start: now(), cgroupBefore: captureBuildCgroupSnapshot()}
}

// child starts a new named step under s, safe to call from concurrent
// goroutines (independent image builds run in waves; independent deploy
// components run in parallel steps).
func (s *stepTiming) child(name string) *stepTiming {
	child := newStepTiming(name, s.now)
	s.mu.Lock()
	s.children = append(s.children, child)
	s.mu.Unlock()
	return child
}

// addFinishedChild records a step whose duration is already known — used for
// per-architecture image builds, where the only timing available is the
// elapsed time the platform loop measured around a builder call that has no
// access to the timing tree itself. cgroup is computed by that same caller
// (it straddles the real docker build subprocess with its own before/after
// samples — see timingPlatformObserver) since this call happens after the
// fact, too late to take a start-of-step sample itself.
func (s *stepTiming) addFinishedChild(name string, elapsed time.Duration, err error, cache *cacheDecision, cgroup *BuildCgroupMetrics) *stepTiming {
	now := s.now()
	child := &stepTiming{name: name, now: s.now, start: now.Add(-elapsed), end: now, ended: true, cache: cache, cgroupMetrics: cgroup}
	if err != nil {
		child.failed = true
		child.errMsg = err.Error()
	}
	s.mu.Lock()
	s.children = append(s.children, child)
	s.mu.Unlock()
	return child
}

func (s *stepTiming) finish(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.end = s.now()
	s.ended = true
	if err != nil {
		s.failed = true
		s.errMsg = err.Error()
	}
	s.cgroupMetrics = buildCgroupMetricsFromSnapshots(s.cgroupBefore, captureBuildCgroupSnapshot(), s.end.Sub(s.start))
}

func (s *stepTiming) setCache(hit bool, missReason string) {
	s.mu.Lock()
	s.cache = &cacheDecision{hit: hit, missReason: missReason}
	s.mu.Unlock()
}

func (s *stepTiming) duration() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	end := s.end
	if !s.ended {
		end = s.now()
	}
	return end.Sub(s.start)
}

// snapshot copies the fields renderers need under the lock, so recursion into
// children never holds a parent's lock while touching a child's.
type stepSnapshot struct {
	name     string
	start    time.Time
	dur      time.Duration
	failed   bool
	errMsg   string
	cache    *cacheDecision
	cgroup   *BuildCgroupMetrics
	children []*stepTiming
}

func (s *stepTiming) snapshot() stepSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	end := s.end
	if !s.ended {
		end = s.now()
	}
	children := make([]*stepTiming, len(s.children))
	copy(children, s.children)
	return stepSnapshot{
		name:     s.name,
		start:    s.start,
		dur:      end.Sub(s.start),
		failed:   s.failed,
		errMsg:   s.errMsg,
		cache:    s.cache,
		cgroup:   s.cgroupMetrics,
		children: children,
	}
}

// timingGap is the difference between a step's own duration and the sum of
// its children's durations. Positive means real time the children don't
// account for (a phase transition, an uninstrumented sub-step); negative
// means the children overlapped (a concurrent build wave), which is expected
// and reported as such rather than as a misleading negative "unaccounted".
func timingGap(step stepSnapshot) time.Duration {
	var childrenTotal time.Duration
	for _, child := range step.children {
		childrenTotal += child.duration()
	}
	return step.dur - childrenTotal
}

// startTimingStep begins a named child step under the context's active
// timing root, when one is running (RunBuildExecution/RunReleaseExecution/
// RunPushCommand/RunDeploySpecs each start one; ctx.timing is nil for every
// other command, and for all of them in dry-run, so this degrades to a no-op
// everywhere else). It returns a context scoped to the new step so nested
// calls attach their own children to it, plus the finish func to call on
// completion.
func (c Context) startTimingStep(name string) (Context, func(error)) {
	if c.timing == nil {
		return c, func(error) {}
	}
	child := c.timing.child(name)
	next := c
	next.timing = child
	return next, func(err error) { child.finish(err) }
}

// recordTimingCache attaches a fingerprint cache hit/miss decision to the
// context's current step (a no-op outside an active timing root).
func (c Context) recordTimingCache(hit bool, missReason string) {
	if c.timing == nil {
		return
	}
	c.timing.setCache(hit, missReason)
}

// timingPlatformObserver returns a callback that records one finished
// per-architecture child under the context's current step, tagged with the
// same cache decision every architecture of one image build shares. It is
// wired onto DockerBuildSpec.PlatformObserver rather than threaded through
// DockerImageBuilderFunc, so builder implementations that build every
// platform in one call (the shared default, and any test double or retry
// wrapper around it) need no signature change to report per-platform timing.
func (c Context) timingPlatformObserver(cache *cacheDecision) func(platform string, elapsed time.Duration, err error, cgroup *BuildCgroupMetrics) {
	if c.timing == nil {
		return func(string, time.Duration, error, *BuildCgroupMetrics) {}
	}
	step := c.timing
	return func(platform string, elapsed time.Duration, err error, cgroup *BuildCgroupMetrics) {
		step.addFinishedChild(platform, elapsed, err, cache, cgroup)
	}
}

// reportStepTiming renders the ordered step-timing table to the command's
// normal feedback channel and writes the machine-readable record alongside
// it. Called once by each of the four umbrellas after root.finish, so it
// always reports the full tree, on success and on failure alike.
func reportStepTiming(ctx Context, command string, root *stepTiming) {
	ctx.Info("step timing (ordered by duration):")
	for _, row := range renderStepTimingRows(root, 0) {
		ctx.Info("  " + row)
	}
	path, err := writeTimingRecord(command, root)
	if err != nil {
		ctx.Trace("timing record: could not write " + command + " record: " + err.Error())
		return
	}
	ctx.Info("timing record written to " + path)
}

// timingRow is either a real step or a synthetic gap row (unaccounted time or
// concurrent overlap); exactly one of step/synthetic is set.
type timingRow struct {
	step      *stepTiming
	synthetic string
	name      string
	dur       time.Duration
}

// timingOrderNoiseFloor is below the smallest gap worth reordering rows for —
// a real build/deploy step differs by seconds at least, so this only ever
// changes the order of two steps whose measured durations are close enough to
// be scheduler jitter rather than signal (concurrent siblings finishing
// within a millisecond of each other, or two near-instant operations in a
// test). Below the floor, rows break ties by name instead of by duration, so
// the same run produces the same order every time — sorting two genuinely
// concurrent children by raw duration would otherwise flip their order
// between runs depending only on which one the scheduler happened to finish
// first. A subprocess integration test measures real wall-clock time with no
// fake clock to inject (the timing root lives inside the compiled binary erun
// runs, not the test process), and two of its near-instant steps (a real `git
// push` against a local bare origin, and the version-bump commit beside it)
// were observed to drift past the previous 100ms floor under the CPU
// contention of a full `make check` run, flipping their reported order
// between otherwise-identical runs. 1s keeps the floor well below any
// genuinely different-duration real step while comfortably covering that
// contention.
const timingOrderNoiseFloor = 1 * time.Second

// orderedTimingRows returns a step's children plus a synthetic gap row (when
// the gap is large enough to matter), sorted by duration descending — with
// ties inside timingOrderNoiseFloor broken by name — so the dominant cost
// within this step is always the first line a reader sees, deterministically.
func orderedTimingRows(step stepSnapshot) []timingRow {
	rows := make([]timingRow, 0, len(step.children)+1)
	for _, child := range step.children {
		rows = append(rows, timingRow{step: child, name: child.name, dur: child.duration()})
	}
	if len(step.children) > 0 {
		gap := timingGap(step)
		switch {
		case gap >= timingOrderNoiseFloor:
			rows = append(rows, timingRow{synthetic: "(unaccounted)", name: "(unaccounted)", dur: gap})
		case gap <= -timingOrderNoiseFloor:
			rows = append(rows, timingRow{synthetic: "(ran concurrently, overlap)", name: "(ran concurrently, overlap)", dur: -gap})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		diff := rows[i].dur - rows[j].dur
		if diff < 0 {
			diff = -diff
		}
		if diff < timingOrderNoiseFloor {
			return rows[i].name < rows[j].name
		}
		return rows[i].dur > rows[j].dur
	})
	return rows
}

// renderStepTimingRows renders every duration inside square brackets —
// `<label> [<duration>]` — a deliberately distinctive shape (nothing else in
// erun's output wraps a bare Go duration in brackets) so the integration
// suite's normalizer can redact just these values without touching an
// unrelated "5m" in help text or a rollout timeout example.
func renderStepTimingRows(step *stepTiming, depth int) []string {
	snap := step.snapshot()
	label := snap.name
	if snap.failed {
		label += " (failed)"
	}
	if snap.cache != nil {
		if snap.cache.hit {
			label += " (cache hit)"
		} else {
			label += " (cache miss: " + snap.cache.missReason + ")"
		}
	}
	row := strings.Repeat("  ", depth) + label + " [" + snap.dur.String() + "]" + buildCgroupSummary(snap.cgroup)
	if snap.failed && snap.errMsg != "" {
		row += " — " + snap.errMsg
	}
	rows := []string{row}
	for _, entry := range orderedTimingRows(snap) {
		if entry.step != nil {
			rows = append(rows, renderStepTimingRows(entry.step, depth+1)...)
			continue
		}
		rows = append(rows, strings.Repeat("  ", depth+1)+entry.synthetic+" ["+entry.dur.String()+"]")
	}
	return rows
}

// TimingRecord is the machine-readable, one-document-per-run counterpart to
// the step-timing table, so two runs (e.g. a fast release and a 22x-slower
// one) can be diffed by tooling instead of compared by eye across logs.
type TimingRecord struct {
	Command         string              `json:"command"`
	StartedAt       time.Time           `json:"startedAt"`
	DurationSeconds float64             `json:"durationSeconds"`
	Duration        string              `json:"duration"`
	Failed          bool                `json:"failed"`
	Error           string              `json:"error,omitempty"`
	Cgroup          *BuildCgroupMetrics `json:"cgroup,omitempty"`
	Steps           []TimingStepJSON    `json:"steps,omitempty"`
}

// TimingStepJSON is one node of the timing tree in the JSON record. Unlike
// the printed table, its children are kept in a stable (insertion) order
// rather than duration-sorted, so a diff between two runs' JSON compares the
// same step in the same place even when a regression changed the ordering a
// human-facing table would show.
type TimingStepJSON struct {
	Name               string              `json:"name"`
	DurationSeconds    float64             `json:"durationSeconds"`
	Duration           string              `json:"duration"`
	Failed             bool                `json:"failed,omitempty"`
	Error              string              `json:"error,omitempty"`
	CacheHit           *bool               `json:"cacheHit,omitempty"`
	CacheMissReason    string              `json:"cacheMissReason,omitempty"`
	UnaccountedSeconds float64             `json:"unaccountedSeconds,omitempty"`
	OverlapSeconds     float64             `json:"overlapSeconds,omitempty"`
	Cgroup             *BuildCgroupMetrics `json:"cgroup,omitempty"`
	Steps              []TimingStepJSON    `json:"steps,omitempty"`
}

func (s *stepTiming) toStepJSON() TimingStepJSON {
	snap := s.snapshot()
	out := TimingStepJSON{
		Name:            snap.name,
		DurationSeconds: snap.dur.Seconds(),
		Duration:        snap.dur.String(),
		Failed:          snap.failed,
		Error:           snap.errMsg,
		Cgroup:          snap.cgroup,
	}
	if snap.cache != nil {
		hit := snap.cache.hit
		out.CacheHit = &hit
		out.CacheMissReason = snap.cache.missReason
	}
	if len(snap.children) > 0 {
		out.Steps = make([]TimingStepJSON, 0, len(snap.children))
		for _, child := range snap.children {
			out.Steps = append(out.Steps, child.toStepJSON())
		}
		gap := timingGap(snap)
		if gap >= timingOrderNoiseFloor {
			out.UnaccountedSeconds = gap.Seconds()
		} else if gap <= -timingOrderNoiseFloor {
			out.OverlapSeconds = (-gap).Seconds()
		}
	}
	return out
}

func (s *stepTiming) toRecord(command string) TimingRecord {
	snap := s.snapshot()
	record := TimingRecord{
		Command:         command,
		StartedAt:       snap.start,
		DurationSeconds: snap.dur.Seconds(),
		Duration:        snap.dur.String(),
		Failed:          snap.failed,
		Error:           snap.errMsg,
		Cgroup:          snap.cgroup,
	}
	for _, child := range snap.children {
		record.Steps = append(record.Steps, child.toStepJSON())
	}
	return record
}

// timingRecordDir is a sibling of the per-env trace.log tree (~/.erun/...)
// rather than the trace.log path itself: build/release/push commonly run
// with no tenant/environment at all (they are pure primitives that do not
// require a deploy target — root AGENTS.md § "Command primitives vs
// orchestration"), so a location keyed to tenant+environment would leave
// most build/release/push runs with no record. A flat, home-relative
// directory gives every one of the four commands the same, always-available
// home, without standing up a queryable store this feature does not need:
// two runs are diffed by reading two small JSON files.
func timingRecordDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".erun", "timing"), nil
}

// timingRecordFileName names one run's record so a directory listing sorts
// oldest-to-newest and never collides with another run of the same command.
func timingRecordFileName(command string, startedAt time.Time) string {
	return command + "-" + startedAt.UTC().Format("20060102T150405.000000000Z") + ".json"
}

// maxTimingRecordsRetained is the number of records kept per command, so a
// before/after comparison is a command reading two small files rather than
// an operator hand-copying numbers out of a log before they scroll away.
// Pruning happens on write, best-effort: a prune failure must not fail the
// build whose record it was about to write.
const maxTimingRecordsRetained = 50

func writeTimingRecord(command string, root *stepTiming) (string, error) {
	dir, err := timingRecordDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	record := root.toRecord(command)
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, timingRecordFileName(command, record.StartedAt))
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return "", err
	}
	pruneTimingRecords(dir, command)
	return path, nil
}

// timingRecordFileNamesForCommand lists a directory's record file names
// belonging to one command, in whatever order os.ReadDir returned them
// (alphabetical -- which sorts chronologically too, since the timestamp in
// the name is zero-padded and UTC).
func timingRecordFileNamesForCommand(entries []os.DirEntry, command string) []string {
	prefix := command + "-"
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".json") {
			names = append(names, name)
		}
	}
	return names
}

// pruneTimingRecords removes a command's oldest records beyond
// maxTimingRecordsRetained. Never fatal: a directory that cannot be listed or
// a file that cannot be removed just means retention doesn't happen this
// time, not that the record just written is lost.
func pruneTimingRecords(dir, command string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	names := timingRecordFileNamesForCommand(entries, command)
	if len(names) <= maxTimingRecordsRetained {
		return
	}
	sort.Strings(names)
	for _, name := range names[:len(names)-maxTimingRecordsRetained] {
		_ = os.Remove(filepath.Join(dir, name))
	}
}

// incrementalCacheDecision derives the same hit/reason a build's fingerprint
// decision already gives the trace (traceIncrementalDecision), so the timing
// report's cache tag never disagrees with it. applicable is false when the
// build has no fingerprint at all (incremental caching disabled or not
// computed for it), which is not a cache miss and should carry no tag.
func incrementalCacheDecision(buildInput DockerBuildSpec) (hit, applicable bool, missReason string) {
	if buildInput.Fingerprint == "" {
		return false, false, ""
	}
	if buildInput.Promote {
		return true, true, ""
	}
	switch {
	case strings.TrimSpace(buildInput.CascadeRebuildFromTag) != "":
		return false, true, "dependency " + strings.TrimSpace(buildInput.CascadeRebuildFromTag) + " is rebuilding"
	case len(buildInput.MissingFingerprintPlatforms) > 0:
		return false, true, "fingerprint image is missing for " + describeMissingPlatforms(buildInput.MissingFingerprintPlatforms)
	default:
		return false, true, "no cached fingerprint image"
	}
}

// dockerBuildStepName is the timing tree's label for one image build — the
// short image name when known, so a table row reads "erun-console" rather
// than the full registry tag.
func dockerBuildStepName(buildInput DockerBuildSpec) string {
	if name := strings.TrimSpace(buildInput.Image.ImageName); name != "" {
		return name
	}
	return strings.TrimSpace(buildInput.Image.Tag)
}
