package eruncommon

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestWriteFileAtomicNeverExposesPartialContentToConcurrentReaders is the
// regression for erun#1774's first acceptance criterion: a concurrent writer
// must never let a reader observe a partial document. writeFileAtomic's
// write-to-temp-then-rename contract is what every Save*Config function in
// config.go already routes through; this pins that a reader racing a stream
// of writes only ever sees one complete write's content, never a mix.
func TestWriteFileAtomicNeverExposesPartialContentToConcurrentReaders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	valueA := []byte(strings.Repeat("a", 4096) + "\n")
	valueB := []byte(strings.Repeat("b", 8192) + "\n")
	if err := writeFileAtomic(path, valueA, 0o644); err != nil {
		t.Fatalf("seed write: %v", err)
	}

	var done atomic.Bool
	var badReads atomic.Int64
	writerDone := make(chan struct{})
	readerDone := make(chan struct{})

	go func() {
		defer close(writerDone)
		for i := 0; !done.Load(); i++ {
			value := valueA
			if i%2 == 1 {
				value = valueB
			}
			if err := writeFileAtomic(path, value, 0o644); err != nil {
				t.Errorf("concurrent write failed: %v", err)
				return
			}
		}
	}()

	go func() {
		defer close(readerDone)
		for !done.Load() {
			data, err := os.ReadFile(path)
			if err != nil {
				// A rename mid-open can race ENOENT on some filesystems; that
				// is not the defect under test (a torn/partial document is).
				continue
			}
			if !bytes.Equal(data, valueA) && !bytes.Equal(data, valueB) {
				badReads.Add(1)
			}
		}
	}()

	time.Sleep(200 * time.Millisecond)
	done.Store(true)
	<-writerDone
	<-readerDone

	if n := badReads.Load(); n != 0 {
		t.Fatalf("observed %d torn/partial reads racing writeFileAtomic", n)
	}
}

// TestLoadConfigFileRetriesATornReadThenSucceeds is the regression for the
// second acceptance criterion: a reader that hits a torn read recovers
// rather than failing the request. It seeds the on-disk residue of an
// interrupted write (a zero-length file, config.go's own documented torn-read
// signature) and heals it mid-retry-budget, mirroring how quickly a real
// concurrent writer's rename actually resolves the race.
func TestLoadConfigFileRetriesATornReadThenSucceeds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("seed torn (zero-length) file: %v", err)
	}

	healed := make(chan struct{})
	go func() {
		defer close(healed)
		time.Sleep(configReadRetrySleep * 2)
		if err := writeFileAtomic(path, []byte("name: healed\n"), 0o644); err != nil {
			t.Errorf("heal write failed: %v", err)
		}
	}()

	var config EnvConfig
	if err := loadConfigFile(path, &config); err != nil {
		t.Fatalf("expected the retry to observe the healed write, got: %v", err)
	}
	if config.Name != "healed" {
		t.Fatalf("unexpected config after a healed retry: %+v", config)
	}
	<-healed
}

// TestLoadConfigFileGenuinelyCorruptFileStillFails proves the retry budget
// does not mask a real corruption: a file that never becomes valid YAML must
// still surface ErrConfigCorrupted, and within a bounded time rather than
// retrying forever.
func TestLoadConfigFileGenuinelyCorruptFileStillFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("not: [valid yaml"), 0o644); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}

	var config EnvConfig
	start := time.Now()
	err := loadConfigFile(path, &config)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrConfigCorrupted) {
		t.Fatalf("expected ErrConfigCorrupted for a file that never becomes valid, got: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("expected the retry budget to be bounded, took: %v", elapsed)
	}
}

// TestLoadConfigFileMissingFileReturnsNotInitializedImmediately confirms a
// missing file is treated as a real absence, not a torn read, and is never
// retried -- retrying it would only slow down every ordinary "not yet
// initialized" path (e.g. ListEnvConfigs skipping an unconfigured entry).
func TestLoadConfigFileMissingFileReturnsNotInitializedImmediately(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	var config EnvConfig
	start := time.Now()
	err := loadConfigFile(path, &config)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("expected ErrNotInitialized for a missing file, got: %v", err)
	}
	if elapsed > 50*time.Millisecond {
		t.Fatalf("expected no retry delay for a missing file, took: %v", elapsed)
	}
}
