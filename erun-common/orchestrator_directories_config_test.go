package eruncommon

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestOrchestratorDirectoriesSurviveARoundTrip pins the on-disk spelling of an
// orchestrator's own directories. The desktop re-marshals the whole root config
// every time a definition is written, so a tag that did not match the field it
// claims would drop every directory the operator had added — silently, on the
// next save, with the directories gone from a definition that still looks fine.
func TestOrchestratorDirectoriesSurviveARoundTrip(t *testing.T) {
	t.Parallel()
	original := ERunConfig{
		Orchestrators: []OrchestratorConfig{{
			ID:   "erun",
			Name: "erun",
			Directories: []OrchestratorDirectoryConfig{
				{Directory: "/Users/operator/src/notes"},
				{Directory: "/Users/operator/src/scratch"},
			},
		}},
	}

	data, err := yaml.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{"directories:", "directory: /Users/operator/src/notes"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("expected %q in the marshalled config, got:\n%s", want, data)
		}
	}

	var decoded ERunConfig
	if err := yaml.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decoded.Orchestrators) != 1 {
		t.Fatalf("expected one orchestrator, got %+v", decoded.Orchestrators)
	}
	dirs := decoded.Orchestrators[0].Directories
	if len(dirs) != 2 || dirs[0].Directory != "/Users/operator/src/notes" || dirs[1].Directory != "/Users/operator/src/scratch" {
		t.Fatalf("directories did not survive the round trip: %+v", dirs)
	}
}
