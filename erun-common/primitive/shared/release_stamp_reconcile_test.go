package shared

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
)

// A retry must walk past its own leftover unpushed stamp commit to mint the same version,
// stopping at anything origin has.

// A retry's stamp files are already at the version, so the release stage carries only the tag command.
func TestNewReleaseStageOmitsCommitWhenNoFileUpdatesAreNeeded(t *testing.T) {
	stage := NewReleaseStage("/tmp/release-stamp-test", nil, "1.2.3", model.ReleaseModeStable)
	for _, command := range stage.GitCommands {
		if len(command.Args) > 0 && command.Args[0] == "commit" {
			t.Fatalf("expected no commit command when no file updates are needed, got %v", command.Args)
		}
	}
	if len(stage.GitCommands) != 1 || stage.GitCommands[0].Args[0] != "tag" {
		t.Fatalf("expected only the tag command, got %v", stage.GitCommands)
	}
}
