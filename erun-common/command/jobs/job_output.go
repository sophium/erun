package jobs

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sophium/erun/erun-common/primitive/shared"
)

// ReadEnvironmentJobOutputParams pages through a job's captured output.
type ReadEnvironmentJobOutputParams struct {
	Tenant      string
	Environment string
	ID          string
	Offset      int64
	MaxBytes    int64
}

// EnvironmentJobOutput is one page of a job's merged stdout and stderr.
type EnvironmentJobOutput struct {
	Job        shared.EnvironmentJob `json:"job"`
	Offset     int64                 `json:"offset"`
	NextOffset int64                 `json:"nextOffset"`
	Output     string                `json:"output"`
	HasMore    bool                  `json:"hasMore"`
	// Complete means the job finished and this page reached the end of its output.
	Complete bool `json:"complete"`
}

// ReadEnvironmentJobOutput returns one page of a job's output, readable while it runs.
func ReadEnvironmentJobOutput(params ReadEnvironmentJobOutputParams) (EnvironmentJobOutput, error) {
	job, err := LoadEnvironmentJob(params.Tenant, params.Environment, params.ID, time.Now())
	if err != nil {
		return EnvironmentJobOutput{}, err
	}
	offset := params.Offset
	if offset < 0 {
		return EnvironmentJobOutput{}, fmt.Errorf("output offset must not be negative")
	}
	maxBytes := params.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultEnvironmentJobOutputReadBytes
	}
	result := EnvironmentJobOutput{Job: job, Offset: offset, NextOffset: offset}
	// No log yet is an empty page, not an error.
	if strings.TrimSpace(job.LogPath) == "" {
		result.Complete = job.Finished()
		return result, nil
	}
	page, size, err := readEnvironmentJobLogPage(job.LogPath, offset, maxBytes)
	if err != nil {
		if os.IsNotExist(err) {
			result.Complete = job.Finished()
			return result, nil
		}
		return EnvironmentJobOutput{}, err
	}
	result.Output = string(page)
	result.NextOffset = offset + int64(len(page))
	result.HasMore = result.NextOffset < size
	result.Complete = job.Finished() && !result.HasMore
	return result, nil
}

// readEnvironmentJobLogPage returns at most maxBytes from offset plus the log's current size.
func readEnvironmentJobLogPage(path string, offset, maxBytes int64) ([]byte, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	size := info.Size()
	if offset > size {
		return nil, 0, fmt.Errorf("output offset %d is past the end of the job log (%d bytes)", offset, size)
	}
	read := size - offset
	if read > maxBytes {
		read = maxBytes
	}
	buffer := make([]byte, read)
	if read > 0 {
		if _, err := file.ReadAt(buffer, offset); err != nil {
			return nil, 0, err
		}
	}
	return buffer, size, nil
}

func environmentJobOutputSize(path string, fallback int64) int64 {
	if strings.TrimSpace(path) == "" {
		return fallback
	}
	info, err := os.Stat(path)
	if err != nil {
		return fallback
	}
	return info.Size()
}

// jobOutputWriter caps the log file but feeds the agent reader every byte, so
// the cap never freezes progress.
type jobOutputWriter struct {
	file       *os.File
	limit      int64
	agent      *agentProgressReader
	onTruncate func()

	mu      sync.Mutex
	count   int64
	dropped bool
}

func (w *jobOutputWriter) Write(p []byte) (int, error) {
	if w.agent != nil {
		w.agent.feed(p)
	}
	return w.writeCapped(p)
}

func (w *jobOutputWriter) writeCapped(p []byte) (int, error) {
	w.mu.Lock()
	room := w.limit - w.count
	if room <= 0 {
		first := !w.dropped
		w.dropped = true
		w.mu.Unlock()
		if first && w.onTruncate != nil {
			w.onTruncate()
		}
		return len(p), nil
	}
	chunk := p
	if int64(len(chunk)) > room {
		chunk = chunk[:room]
	}
	written, err := w.file.Write(chunk)
	w.count += int64(written)
	hitCap := w.count >= w.limit && !w.dropped
	if hitCap {
		w.dropped = true
	}
	w.mu.Unlock()
	if hitCap && w.onTruncate != nil {
		w.onTruncate()
	}
	if err != nil {
		return written, err
	}
	return len(p), nil
}

func (w *jobOutputWriter) written() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.count
}

func (w *jobOutputWriter) truncated() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dropped
}
