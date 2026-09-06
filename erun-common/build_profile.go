package eruncommon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// TimingRecordSummary is one entry in a recent-builds listing -- lightweight
// enough to build without parsing every retained record's full step tree, so
// `erun build profile` (root AGENTS.md #2274) can list history cheaply before
// a caller picks one record to see in full.
type TimingRecordSummary struct {
	ID              string  `json:"id"`
	StartedAt       string  `json:"startedAt"`
	DurationSeconds float64 `json:"durationSeconds"`
	Failed          bool    `json:"failed"`
}

// ListTimingRecords returns command's retained records, newest first. limit
// <= 0 returns every retained record (bounded by maxTimingRecordsRetained,
// since that is all writeTimingRecord ever keeps on disk). A record file that
// fails to parse is skipped rather than failing the whole listing -- a
// half-written or corrupted record must not hide every other one.
func ListTimingRecords(command string, limit int) ([]TimingRecordSummary, error) {
	dir, err := timingRecordDir()
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
	names := timingRecordFileNamesForCommand(entries, command)
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if limit > 0 && len(names) > limit {
		names = names[:limit]
	}
	summaries := make([]TimingRecordSummary, 0, len(names))
	for _, name := range names {
		record, err := loadTimingRecordFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		summaries = append(summaries, TimingRecordSummary{
			ID:              strings.TrimSuffix(name, ".json"),
			StartedAt:       record.StartedAt.UTC().Format("2006-01-02T15:04:05Z"),
			DurationSeconds: record.DurationSeconds,
			Failed:          record.Failed,
		})
	}
	return summaries, nil
}

// LoadTimingRecord resolves id -- a record's ID as printed by
// ListTimingRecords, with or without its .json suffix, or "" / "latest" for
// command's most recent retained record -- and returns the parsed record.
func LoadTimingRecord(command, id string) (TimingRecord, error) {
	dir, err := timingRecordDir()
	if err != nil {
		return TimingRecord{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" || id == "latest" {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return TimingRecord{}, fmt.Errorf("no %s timing records found in %s", command, dir)
			}
			return TimingRecord{}, err
		}
		names := timingRecordFileNamesForCommand(entries, command)
		if len(names) == 0 {
			return TimingRecord{}, fmt.Errorf("no %s timing records found in %s", command, dir)
		}
		sort.Strings(names)
		id = strings.TrimSuffix(names[len(names)-1], ".json")
	}
	if !strings.HasSuffix(id, ".json") {
		id += ".json"
	}
	return loadTimingRecordFile(filepath.Join(dir, id))
}

func loadTimingRecordFile(path string) (TimingRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return TimingRecord{}, err
	}
	var record TimingRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return TimingRecord{}, err
	}
	return record, nil
}

// RenderTimingRecordRows renders a loaded TimingRecord as the same
// duration-ordered, cgroup-annotated table reportStepTiming prints live
// during a run, so `erun build profile <id>` reads identically to the
// original run's own "step timing" output.
func RenderTimingRecordRows(record TimingRecord) []string {
	label := record.Command
	if record.Failed {
		label += " (failed)"
	}
	row := label + " [" + record.Duration + "]" + buildCgroupSummary(record.Cgroup)
	if record.Failed && record.Error != "" {
		row += " — " + record.Error
	}
	rows := []string{row}
	for _, child := range orderedTimingStepJSONRows(record.Steps) {
		rows = append(rows, renderTimingStepJSONRows(child, 1)...)
	}
	return rows
}

// orderedTimingStepJSONRows sorts a record's stably-ordered (insertion-order)
// JSON children by duration descending, mirroring orderedTimingRows' live
// equivalent -- the dominant cost is always the first line a reader sees.
func orderedTimingStepJSONRows(steps []TimingStepJSON) []TimingStepJSON {
	sorted := make([]TimingStepJSON, len(steps))
	copy(sorted, steps)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].DurationSeconds > sorted[j].DurationSeconds
	})
	return sorted
}

func renderTimingStepJSONRows(step TimingStepJSON, depth int) []string {
	label := step.Name
	if step.Failed {
		label += " (failed)"
	}
	if step.CacheHit != nil {
		if *step.CacheHit {
			label += " (cache hit)"
		} else {
			label += " (cache miss: " + step.CacheMissReason + ")"
		}
	}
	row := strings.Repeat("  ", depth) + label + " [" + step.Duration + "]" + buildCgroupSummary(step.Cgroup)
	if step.Failed && step.Error != "" {
		row += " — " + step.Error
	}
	rows := []string{row}
	for _, child := range orderedTimingStepJSONRows(step.Steps) {
		rows = append(rows, renderTimingStepJSONRows(child, depth+1)...)
	}
	if step.UnaccountedSeconds > 0 {
		rows = append(rows, strings.Repeat("  ", depth+1)+fmt.Sprintf("(unaccounted) [%s]", formatSecondsAsDuration(step.UnaccountedSeconds)))
	}
	if step.OverlapSeconds > 0 {
		rows = append(rows, strings.Repeat("  ", depth+1)+fmt.Sprintf("(ran concurrently, overlap) [%s]", formatSecondsAsDuration(step.OverlapSeconds)))
	}
	return rows
}

func formatSecondsAsDuration(seconds float64) string {
	return time.Duration(seconds * float64(time.Second)).String()
}
