package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
)

// stepTiming is one measured step in a command's run, arranged as a tree.
type stepTiming struct {
	mu            sync.Mutex
	Name          string
	now           func() time.Time
	start         time.Time
	end           time.Time
	ended         bool
	failed        bool
	errMsg        string
	cache         *CacheDecision
	Children      []*stepTiming
	cgroupBefore  BuildCgroupSnapshot
	cgroupMetrics *model.BuildCgroupMetrics
}

// CacheDecision records whether an image build hit the fingerprint cache and why.
// It is shared and mutable: a promote that later falls back to a real build
// corrects the hit it already recorded.
type CacheDecision struct {
	mu         sync.Mutex
	Hit        bool
	MissReason string
}

// Decision reads hit and reason together so they never disagree.
func (c *CacheDecision) Decision() (bool, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Hit, c.MissReason
}

// NotePromoteFallback turns a recorded hit into a miss after the promote fell
// back to a real build.
func (c *CacheDecision) NotePromoteFallback() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Hit = false
	c.MissReason = PromoteFallbackMissReason
}

// PromoteFallbackMissReason is the miss reason a corrected decision carries.
const PromoteFallbackMissReason = "the promoted fingerprint image could not be published; rebuilt from source"

func NewStepTiming(name string, now func() time.Time) *stepTiming {
	if now == nil {
		now = time.Now
	}
	return &stepTiming{Name: name, now: now, start: now(), cgroupBefore: CaptureBuildCgroupSnapshot()}
}

// Child starts a named step under s; safe for concurrent use.
func (s *stepTiming) Child(name string) *stepTiming {
	child := NewStepTiming(name, s.now)
	s.mu.Lock()
	s.Children = append(s.Children, child)
	s.mu.Unlock()
	return child
}

// AddFinishedChild records a step whose duration and cgroup metrics the caller
// already measured.
func (s *stepTiming) AddFinishedChild(name string, elapsed time.Duration, err error, cache *CacheDecision, cgroup *model.BuildCgroupMetrics) *stepTiming {
	now := s.now()
	child := &stepTiming{Name: name, now: s.now, start: now.Add(-elapsed), end: now, ended: true, cache: cache, cgroupMetrics: cgroup}
	if err != nil {
		child.failed = true
		child.errMsg = err.Error()
	}
	s.mu.Lock()
	s.Children = append(s.Children, child)
	s.mu.Unlock()
	return child
}

func (s *stepTiming) Finish(err error) {
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
	s.cgroupMetrics = BuildCgroupMetricsFromSnapshots(s.cgroupBefore, CaptureBuildCgroupSnapshot(), s.end.Sub(s.start))
}

// setCache stores the pointer so every row sharing it sees a later correction.
func (s *stepTiming) setCache(cache *CacheDecision) {
	s.mu.Lock()
	s.cache = cache
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

// stepSnapshot is copied under the lock so recursion never holds a parent's lock.
type stepSnapshot struct {
	name     string
	Start    time.Time
	dur      time.Duration
	failed   bool
	errMsg   string
	cache    *CacheDecision
	cgroup   *model.BuildCgroupMetrics
	children []*stepTiming
}

func (s *stepTiming) Snapshot() stepSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	end := s.end
	if !s.ended {
		end = s.now()
	}
	children := make([]*stepTiming, len(s.Children))
	copy(children, s.Children)
	return stepSnapshot{
		name:     s.Name,
		Start:    s.start,
		dur:      end.Sub(s.start),
		failed:   s.failed,
		errMsg:   s.errMsg,
		cache:    s.cache,
		cgroup:   s.cgroupMetrics,
		children: children,
	}
}

// timingGap is the step's duration minus its children's; negative means overlap.
func timingGap(step stepSnapshot) time.Duration {
	var childrenTotal time.Duration
	for _, child := range step.children {
		childrenTotal += child.duration()
	}
	return step.dur - childrenTotal
}

// StartTimingStep begins a child step under the active timing root, or is a
// no-op when none is running.
func (c Context) StartTimingStep(name string) (Context, func(error)) {
	if c.Timing == nil {
		return c, func(error) {}
	}
	child := c.Timing.Child(name)
	next := c
	next.Timing = child
	return next, func(err error) { child.Finish(err) }
}

// RecordTimingCache attaches cache to the current step.
func (c Context) RecordTimingCache(cache *CacheDecision) {
	if c.Timing == nil {
		return
	}
	c.Timing.setCache(cache)
}

// TimingPlatformObserver records each per-architecture build as a child of the
// current step, broken down into its BuildKit steps.
func (c Context) TimingPlatformObserver(cache *CacheDecision) func(platform string, elapsed time.Duration, err error, cgroup *model.BuildCgroupMetrics, buildOutput string) {
	if c.Timing == nil {
		return func(string, time.Duration, error, *model.BuildCgroupMetrics, string) {}
	}
	step := c.Timing
	return func(platform string, elapsed time.Duration, err error, cgroup *model.BuildCgroupMetrics, buildOutput string) {
		child := step.AddFinishedChild(platform, elapsed, err, cache, cgroup)
		AttachBuildProgressPhases(child, buildOutput)
	}
}

// AttachBuildProgressPhases adds the parsed Dockerfile steps as finished children of step.
func AttachBuildProgressPhases(step *stepTiming, buildOutput string) {
	for _, phase := range BuildKitProgressPhases(buildOutput) {
		attachProgressPhase(step, phase)
	}
}

func attachProgressPhase(parent *stepTiming, phase BuildProgressPhase) {
	child := parent.AddFinishedChild(phase.Name, phase.Duration, nil, nil, nil)
	for _, sub := range phase.Children {
		attachProgressPhase(child, sub)
	}
}

// ReportStepTiming prints the timing table and writes the JSON record.
func ReportStepTiming(ctx Context, command string, root *stepTiming) {
	ctx.Info("step timing (ordered by duration):")
	for _, row := range RenderStepTimingRows(root, 0) {
		// Indent every line of the row: the integration canonicalizer uses indentation
		// to keep a multi-line row together when sorting siblings.
		ctx.Info("  " + strings.ReplaceAll(row, "\n", "\n  "))
	}
	path, err := WriteTimingRecord(command, root)
	if err != nil {
		ctx.Trace("timing record: could not write " + command + " record: " + err.Error())
		return
	}
	ctx.Info("timing record written to " + path)
}

// timingRow holds exactly one of step or synthetic.
type timingRow struct {
	step      *stepTiming
	synthetic string
	name      string
	dur       time.Duration
}

// timingOrderNoiseFloor: rows closer than this are ordered by name, so jitter
// does not flip their order between runs.
const timingOrderNoiseFloor = 1 * time.Second

// orderedTimingRows returns the children plus any gap row, longest first.
func orderedTimingRows(step stepSnapshot) []timingRow {
	rows := make([]timingRow, 0, len(step.children)+1)
	for _, child := range step.children {
		rows = append(rows, timingRow{step: child, name: child.Name, dur: child.duration()})
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

// RenderStepTimingRows wraps durations in brackets so the integration suite's
// normalizer can redact exactly these values.
func RenderStepTimingRows(step *stepTiming, depth int) []string {
	snap := step.Snapshot()
	label := snap.name
	if snap.failed {
		label += " (failed)"
	}
	if snap.cache != nil {
		if hit, missReason := snap.cache.Decision(); hit {
			label += " (cache hit)"
		} else {
			label += " (cache miss: " + missReason + ")"
		}
	}
	row := strings.Repeat("  ", depth) + label + " [" + snap.dur.String() + "]" + BuildCgroupSummary(snap.cgroup)
	if snap.failed && snap.errMsg != "" {
		row += " — " + snap.errMsg
	}
	rows := []string{row}
	for _, entry := range orderedTimingRows(snap) {
		if entry.step != nil {
			rows = append(rows, RenderStepTimingRows(entry.step, depth+1)...)
			continue
		}
		rows = append(rows, strings.Repeat("  ", depth+1)+entry.synthetic+" ["+entry.dur.String()+"]")
	}
	return rows
}

func (s *stepTiming) toStepJSON() model.TimingStepJSON {
	snap := s.Snapshot()
	out := model.TimingStepJSON{
		Name:            snap.name,
		DurationSeconds: snap.dur.Seconds(),
		Duration:        snap.dur.String(),
		Failed:          snap.failed,
		Error:           snap.errMsg,
		Cgroup:          snap.cgroup,
	}
	if snap.cache != nil {
		hit, missReason := snap.cache.Decision()
		out.CacheHit = &hit
		out.CacheMissReason = missReason
	}
	if len(snap.children) > 0 {
		out.Steps = make([]model.TimingStepJSON, 0, len(snap.children))
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

func (s *stepTiming) ToRecord(command string) model.TimingRecord {
	snap := s.Snapshot()
	record := model.TimingRecord{
		Command:         command,
		StartedAt:       snap.Start,
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

// TimingRecordDirEnv relocates timing history; test suites set it so they never
// write into the operator's real history.
const TimingRecordDirEnv = "ERUN_TIMING_DIR"

// TimingRecordDir is a seam so tests can redirect records without moving HOME.
var TimingRecordDir = defaultTimingRecordDir

// defaultTimingRecordDir is not keyed by tenant/environment because
// build/release/push often run without one.
func defaultTimingRecordDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(TimingRecordDirEnv)); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".erun", "timing"), nil
}

// TimingRecordFileName sorts chronologically and is unique per run.
func TimingRecordFileName(command string, startedAt time.Time) string {
	return command + "-" + startedAt.UTC().Format("20060102T150405.000000000Z") + ".json"
}

// MaxTimingRecordsRetained is the per-command record count kept on write.
const MaxTimingRecordsRetained = 50

func WriteTimingRecord(command string, root *stepTiming) (string, error) {
	dir, err := TimingRecordDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	record := root.ToRecord(command)
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, TimingRecordFileName(command, record.StartedAt))
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return "", err
	}
	PruneTimingRecords(dir, command)
	return path, nil
}

// TimingRecordFileNamesForCommand returns command's record names; alphabetical
// order is chronological.
func TimingRecordFileNamesForCommand(entries []os.DirEntry, command string) []string {
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

// PruneTimingRecords removes a command's oldest records; failures are ignored.
func PruneTimingRecords(dir, command string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	names := TimingRecordFileNamesForCommand(entries, command)
	if len(names) <= MaxTimingRecordsRetained {
		return
	}
	sort.Strings(names)
	for _, name := range names[:len(names)-MaxTimingRecordsRetained] {
		_ = os.Remove(filepath.Join(dir, name))
	}
}
