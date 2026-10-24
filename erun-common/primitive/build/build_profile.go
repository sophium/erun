package build

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

// TimingRecordSummary is one entry in a recent-builds listing.
type TimingRecordSummary struct {
	ID              string  `json:"id"`
	StartedAt       string  `json:"startedAt"`
	DurationSeconds float64 `json:"durationSeconds"`
	Failed          bool    `json:"failed"`
}

// ListTimingRecords returns command's retained records, newest first; limit <= 0 returns all.
// Unparseable records are skipped so one corrupt file can't hide the rest.
func ListTimingRecords(command string, limit int) ([]TimingRecordSummary, error) {
	dir, err := core.TimingRecordDir()
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
	names := core.TimingRecordFileNamesForCommand(entries, command)
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if limit > 0 && len(names) > limit {
		names = names[:limit]
	}
	summaries := make([]TimingRecordSummary, 0, len(names))
	for _, name := range names {
		record, err := LoadTimingRecordFile(filepath.Join(dir, name))
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

// LoadTimingRecord loads a record by ID (with or without .json), or the latest for "" / "latest".
func LoadTimingRecord(command, id string) (core.TimingRecord, error) {
	dir, err := core.TimingRecordDir()
	if err != nil {
		return core.TimingRecord{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" || id == "latest" {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				return core.TimingRecord{}, fmt.Errorf("no %s timing records found in %s", command, dir)
			}
			return core.TimingRecord{}, err
		}
		names := core.TimingRecordFileNamesForCommand(entries, command)
		if len(names) == 0 {
			return core.TimingRecord{}, fmt.Errorf("no %s timing records found in %s", command, dir)
		}
		sort.Strings(names)
		id = strings.TrimSuffix(names[len(names)-1], ".json")
	}
	if !strings.HasSuffix(id, ".json") {
		id += ".json"
	}
	return LoadTimingRecordFile(filepath.Join(dir, id))
}

func LoadTimingRecordFile(path string) (core.TimingRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return core.TimingRecord{}, err
	}
	var record core.TimingRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return core.TimingRecord{}, err
	}
	return record, nil
}

// RenderTimingRecordRows renders a record identically to the live step timing table.
func RenderTimingRecordRows(record core.TimingRecord) []string {
	label := record.Command
	if record.Failed {
		label += " (failed)"
	}
	row := label + " [" + record.Duration + "]" + core.BuildCgroupSummary(record.Cgroup)
	if record.Failed && record.Error != "" {
		row += " — " + record.Error
	}
	rows := []string{row}
	for _, child := range orderedTimingStepJSONRows(record.Steps) {
		rows = append(rows, renderTimingStepJSONRows(child, 1)...)
	}
	return rows
}

func orderedTimingStepJSONRows(steps []core.TimingStepJSON) []core.TimingStepJSON {
	sorted := make([]core.TimingStepJSON, len(steps))
	copy(sorted, steps)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].DurationSeconds > sorted[j].DurationSeconds
	})
	return sorted
}

func renderTimingStepJSONRows(step core.TimingStepJSON, depth int) []string {
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
	row := strings.Repeat("  ", depth) + label + " [" + step.Duration + "]" + core.BuildCgroupSummary(step.Cgroup)
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
