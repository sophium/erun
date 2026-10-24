package orchestrator

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"gopkg.in/yaml.v3"
)

// TestOrchestratorDirectoriesSurviveARoundTrip: the desktop re-marshals the whole root config, so a
// wrong tag silently drops directories.
func TestOrchestratorDirectoriesSurviveARoundTrip(t *testing.T) {
	t.Parallel()
	original := config.ERunConfig{
		Orchestrators: []config.OrchestratorConfig{{
			ID:   "erun",
			Name: "erun",
			Directories: []config.OrchestratorDirectoryConfig{
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

	var decoded config.ERunConfig
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
