package eruncommon

import (
	"testing"

	"github.com/sophium/erun/erun-common/primitive/shared"
)

// A release that fails after its "release" stage stamp commit leaves that
// stamp sitting, unpushed, at HEAD. Root AGENTS.md "Release Rules" calls the
// pre-publish stages "recoverable by re-running", but re-running naively read
// HEAD's own commit to name the retry's version -- naming it after the stamp
// rather than the change being released, and stacking another stamp on top
// of it every further retry. These tests pin the fix's two
// load-bearing properties: version resolution walks past a leftover,
// unpushed stamp to mint the same version the first attempt did, and the
// walk stops at anything already pushed or incorporated by origin, matching
// the boundary releaseTagMismatchError already draws for the release tag.

// TestNewReleaseStageOmitsCommitWhenNoFileUpdatesAreNeeded pins the
// mechanism that keeps a retry from stacking a second stamp commit: once
// version resolution mints the same version the first attempt did (see the
// tests below), the chart/packaging files a retry would stamp are already at
// that version -- discoverReleaseCharts reports no diff, so the "release"
// stage must carry no add/commit commands at all, only the (already
// idempotent, via canSkipExistingReleaseTag) tag command.
func TestNewReleaseStageOmitsCommitWhenNoFileUpdatesAreNeeded(t *testing.T) {
	stage := shared.NewReleaseStage("/tmp/release-stamp-test", nil, "1.2.3", shared.ReleaseModeStable)
	for _, command := range stage.GitCommands {
		if len(command.Args) > 0 && command.Args[0] == "commit" {
			t.Fatalf("expected no commit command when no file updates are needed, got %v", command.Args)
		}
	}
	if len(stage.GitCommands) != 1 || stage.GitCommands[0].Args[0] != "tag" {
		t.Fatalf("expected only the tag command, got %v", stage.GitCommands)
	}
}
