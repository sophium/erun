package jobs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
)

// A never-created directory must marshal Entries as "[]", not "null".
func TestResolveLocalOutputsEmptyDirectorySerializesAsEmptyArray(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "never-created")

	result, err := ResolveLocalOutputs(model.RuntimeOutputsParams{Dir: dir})
	if err != nil {
		t.Fatalf("resolve local outputs for missing dir: %v", err)
	}
	if len(result.Entries) != 0 {
		t.Fatalf("want no entries, got %+v", result.Entries)
	}
	if result.Entries == nil {
		t.Fatalf("want a non-nil empty slice so JSON marshals to [], got a nil slice which marshals to null")
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if string(decoded["entries"]) != "[]" {
		t.Fatalf("want entries to marshal as [], got %s", decoded["entries"])
	}
}

func TestResolveLocalOutputsNonEmptySerializesAsArray(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	result, err := ResolveLocalOutputs(model.RuntimeOutputsParams{Dir: dir})
	if err != nil {
		t.Fatalf("resolve local outputs: %v", err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	var roundTripped model.RuntimeOutputsListResult
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(roundTripped.Entries) != 1 || roundTripped.Entries[0].Name != "notes.txt" {
		t.Fatalf("want one round-tripped entry notes.txt, got %+v", roundTripped.Entries)
	}
}
