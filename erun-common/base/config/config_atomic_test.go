package config

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

func TestWriteFileAtomicNeverExposesPartialContentToConcurrentReaders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	valueA := []byte(strings.Repeat("a", 4096) + "\n")
	valueB := []byte(strings.Repeat("b", 8192) + "\n")
	if err := WriteFileAtomic(path, valueA, 0o644); err != nil {
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
			if err := WriteFileAtomic(path, value, 0o644); err != nil {
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
				// ENOENT during a rename is not the defect under test.
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
		t.Fatalf("observed %d torn/partial reads racing WriteFileAtomic", n)
	}
}

// The file is healed via configReadRetryObserved rather than a racing writer, which flakes under load.
func TestLoadConfigFileRetriesATornReadThenSucceeds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("seed torn (zero-length) file: %v", err)
	}

	healed := false
	ConfigReadRetryObserved = func(int) {
		if healed {
			return
		}
		healed = true
		if err := WriteFileAtomic(path, []byte("name: healed\n"), 0o644); err != nil {
			t.Errorf("heal write failed: %v", err)
		}
	}
	t.Cleanup(func() { ConfigReadRetryObserved = nil })

	var config EnvConfig
	if err := LoadConfigFile(path, &config); err != nil {
		t.Fatalf("expected the retry to observe the healed write, got: %v", err)
	}
	if config.Name != "healed" {
		t.Fatalf("unexpected config after a healed retry: %+v", config)
	}
	if !healed {
		t.Fatal("expected the retry hook to have run")
	}
}

func TestLoadConfigFileGenuinelyCorruptFileStillFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("not: [valid yaml"), 0o644); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}

	var config EnvConfig
	start := time.Now()
	err := LoadConfigFile(path, &config)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrConfigCorrupted) {
		t.Fatalf("expected ErrConfigCorrupted for a file that never becomes valid, got: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("expected the retry budget to be bounded, took: %v", elapsed)
	}
}

func TestLoadConfigFileMissingFileReturnsNotInitializedImmediately(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	var config EnvConfig
	start := time.Now()
	err := LoadConfigFile(path, &config)
	elapsed := time.Since(start)

	if !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("expected ErrNotInitialized for a missing file, got: %v", err)
	}
	if elapsed > 50*time.Millisecond {
		t.Fatalf("expected no retry delay for a missing file, took: %v", elapsed)
	}
}

func TestLoadConfigFileUnreadablePathReturnsRealErrorNotNotInitialized(t *testing.T) {
	dir := t.TempDir()
	// A directory fails os.ReadFile without relying on permissions, which root ignores.
	path := filepath.Join(dir, "config.yaml")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("seed directory at config path: %v", err)
	}

	var config EnvConfig
	err := LoadConfigFile(path, &config)
	if err == nil {
		t.Fatal("expected an error reading a directory as a config file")
	}
	if errors.Is(err, ErrNotInitialized) {
		t.Fatalf("a read failure that is not a real absence must not be reported as ErrNotInitialized, got: %v", err)
	}
	if errors.Is(err, ErrConfigCorrupted) {
		t.Fatalf("a read failure is not corruption either, got: %v", err)
	}
}
