package podruntime

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

// The go build cache is bounded by size here, evicting the entries that have
// gone longest unused rather than clearing the directory.
//
// Go bounds its own build cache only by age: cmd/go trims entries whose mtime is
// older than five days, at most once a day, and refreshes an entry's mtime on
// use (at most hourly), so those mtimes mean "last used". That is a recency rule
// with no ceiling, and the five-day working set of a multi-module repository
// built in several variants reaches tens of gigabytes. The cache lives on the
// environment's home volume, whose declared size is not a quota on a node-local
// storage class, so several environments growing it put their node under
// DiskPressure.
//
// Clearing would bound it too, and make every later build cold. Evicting by
// last-use mtime keeps exactly the entries the next build wants. Only Go's own
// entries are candidates -- names ending in -a or -d one directory down, the set
// cmd/go's own trim removes -- so the cache's bookkeeping (trim.txt, README,
// log.txt) stays, and trim.txt is not rewritten: this is not the age trim, and
// claiming one ran would suppress the one Go does on its own.
//
// An entry removed between one build writing it and the next reading it is a
// cache miss, which the go command tolerates; it never makes a build fail.

// DefaultGoBuildCacheMaxBytes is the cap the runtime pod applies when the chart
// renders none (16 GiB).
const DefaultGoBuildCacheMaxBytes int64 = 16 << 30

// goBuildCacheTrimPasses bounds how often one trim re-reads the cache. An entry
// written while a pass runs can leave the cache over its cap again; a pass that
// frees nothing ends the trim at once, so this is slack, not a retry loop.
const goBuildCacheTrimPasses = 4

// GoBuildCacheTrimResult is one trim's outcome.
type GoBuildCacheTrimResult struct {
	Dir         string `json:"dir"`
	MaxBytes    int64  `json:"maxBytes"`
	BeforeBytes int64  `json:"beforeBytes"`
	AfterBytes  int64  `json:"afterBytes"`
	Evicted     int    `json:"evicted"`
	Passes      int    `json:"passes"`
	DryRun      bool   `json:"dryRun"`
	// Missing reports a cache directory that does not exist, which holds nothing.
	Missing bool `json:"missing,omitempty"`
}

type goBuildCacheEntry struct {
	path  string
	size  int64
	mtime time.Time
}

// TrimGoBuildCache brings the go build cache at dir under maxBytes. A cache
// already within its cap is not touched and reports nothing; a missing one is
// not created. Under ctx.DryRun nothing is removed and the result counts what
// would have been evicted.
func TrimGoBuildCache(ctx core.Context, dir string, maxBytes int64) (GoBuildCacheTrimResult, error) {
	result := GoBuildCacheTrimResult{Dir: dir, MaxBytes: maxBytes, DryRun: ctx.DryRun}
	if strings.TrimSpace(dir) == "" {
		return result, errors.New("the go build cache directory is empty")
	}
	if maxBytes <= 0 {
		return result, fmt.Errorf("the go build cache cap must be a positive number of bytes, got %d", maxBytes)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		result.Missing = true
		return result, nil
	}
	before, err := goBuildCacheSize(dir)
	if err != nil {
		return result, err
	}
	result.BeforeBytes, result.AfterBytes = before, before
	if before <= maxBytes {
		return result, nil
	}
	if ctx.DryRun {
		plan, err := goBuildCacheEvictionPlan(dir, before-maxBytes)
		result.Evicted = len(plan)
		if err == nil {
			ctx.Info(fmt.Sprintf("erun: %d bytes of go build cache at %s are over the %d byte cap; %d least recently used entries would be evicted (--dry-run, nothing removed)", before, dir, maxBytes, len(plan)))
		}
		return result, err
	}
	if err := trimGoBuildCachePasses(dir, maxBytes, &result); err != nil {
		return result, err
	}
	reportGoBuildCacheTrim(ctx, result)
	return result, nil
}

func trimGoBuildCachePasses(dir string, maxBytes int64, result *GoBuildCacheTrimResult) error {
	for result.Passes < goBuildCacheTrimPasses {
		now, err := goBuildCacheSize(dir)
		if err != nil {
			return err
		}
		result.AfterBytes = now
		if now <= maxBytes {
			return nil
		}
		plan, err := goBuildCacheEvictionPlan(dir, now-maxBytes)
		if err != nil {
			return err
		}
		result.Passes++
		if len(plan) == 0 {
			return nil
		}
		for _, entry := range plan {
			if err := os.RemoveAll(entry.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		result.Evicted += len(plan)
	}
	after, err := goBuildCacheSize(dir)
	result.AfterBytes = after
	return err
}

func reportGoBuildCacheTrim(ctx core.Context, result GoBuildCacheTrimResult) {
	ctx.Info(fmt.Sprintf("erun: trimmed the go build cache at %s from %d to %d bytes, evicting %d least recently used entries against a %d byte cap", result.Dir, result.BeforeBytes, result.AfterBytes, result.Evicted, result.MaxBytes))
	if result.AfterBytes > result.MaxBytes {
		ctx.Info(fmt.Sprintf("erun: the go build cache at %s is still %d bytes over its %d byte cap after %d passes; its remaining entries are not the ones this bound can free", result.Dir, result.AfterBytes, result.MaxBytes, result.Passes))
	}
}

// goBuildCacheEvictionPlan lists the least recently used entries whose sizes
// first cover want bytes, oldest first.
func goBuildCacheEvictionPlan(dir string, want int64) ([]goBuildCacheEntry, error) {
	entries, err := goBuildCacheEntries(dir)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].mtime.Equal(entries[j].mtime) {
			return entries[i].path < entries[j].path
		}
		return entries[i].mtime.Before(entries[j].mtime)
	})
	var plan []goBuildCacheEntry
	var total int64
	for _, entry := range entries {
		if total >= want {
			break
		}
		plan = append(plan, entry)
		total += entry.size
	}
	return plan, nil
}

// goBuildCacheEntries reads every go cache entry: <dir>/<xx>/<hash>-a|-d. An
// executable entry is a directory, so its size is what it holds.
func goBuildCacheEntries(dir string) ([]goBuildCacheEntry, error) {
	subdirs, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var entries []goBuildCacheEntry
	for _, sub := range subdirs {
		if !sub.IsDir() {
			continue
		}
		names, err := os.ReadDir(filepath.Join(dir, sub.Name()))
		if err != nil {
			continue
		}
		for _, name := range names {
			if !strings.HasSuffix(name.Name(), "-a") && !strings.HasSuffix(name.Name(), "-d") {
				continue
			}
			path := filepath.Join(dir, sub.Name(), name.Name())
			info, err := name.Info()
			if err != nil {
				continue
			}
			size, err := goBuildCacheSize(path)
			if err != nil {
				continue
			}
			entries = append(entries, goBuildCacheEntry{path: path, size: size, mtime: info.ModTime()})
		}
	}
	return entries, nil
}

// goBuildCacheSize sums the sizes of the files under path. Directories' own
// sizes are left out: they are filesystem bookkeeping no eviction can free, and
// counting them would make the same cache read differently on every filesystem.
func goBuildCacheSize(path string) (int64, error) {
	var total int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total, err
}
