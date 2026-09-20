package eruncommon

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The drive's OPEN -> READY rung is a shell block inside the erun-merge skill,
// and nothing generates it: no constant here, no CLI help string, no template.
// That block is the only place the platform's READY transition is actually
// driven from, so the build policy it encodes is real behavior even though it
// is written as prose.
//
// The policy is erun-common's own build contract: a released build always
// targets every supported architecture and publishes, while a pre-merge build
// asserts only that the commit builds. Reaching for the release form at this
// rung buys a two-architecture `-pr.<sha>` image and chart publish per pull
// request that nothing consumes -- the artifact that ships is cut after merge
// -- and discards the project's configured platform narrowing, which a release
// never consults. Nothing but this test keeps the skill from drifting back to
// it, and the drift is invisible: the rung still goes green, just several
// minutes and one emulated architecture later.
var mergeReadyRungSkillPaths = []string{
	"../erun-skills/skills/erun-merge/SKILL.md",
}

var (
	fencedSHBlockPattern  = regexp.MustCompile("(?s)```sh\n(.*?)```")
	shellCommentLinePatrn = regexp.MustCompile(`(?m)^\s*#.*$`)
	erunBuildInvocation   = regexp.MustCompile(`erun build\b[^\n|]*`)
)

// mergeReadyRungBlocks returns every shell block in the skill that drives the
// review's own transition, i.e. that calls `erun review record-build`. Failing
// on an empty result matters more than it looks: the rest of the assertions
// are "nothing in this block does X", which a block that was renamed,
// restructured, or moved out of a ```sh fence would satisfy vacuously.
func mergeReadyRungBlocks(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var blocks []string
	for _, match := range fencedSHBlockPattern.FindAllStringSubmatch(string(raw), -1) {
		if strings.Contains(match[1], "erun review record-build") {
			blocks = append(blocks, match[1])
		}
	}
	if len(blocks) == 0 {
		t.Fatalf("%s carries no ```sh block invoking `erun review record-build`; this guard is no longer reading the READY rung it exists to protect", path)
	}
	return blocks
}

// buildInvocations returns the executable `erun build` invocations in a shell
// block, with `#` comment lines dropped so that prose explaining why a release
// is wrong here does not read as one.
func buildInvocations(block string) []string {
	return erunBuildInvocation.FindAllString(shellCommentLinePatrn.ReplaceAllString(block, ""), -1)
}

func TestMergeSkillReadyRungBuildsWithoutPublishingARelease(t *testing.T) {
	for _, path := range mergeReadyRungSkillPaths {
		for _, block := range mergeReadyRungBlocks(t, path) {
			invocations := buildInvocations(block)
			if len(invocations) == 0 {
				t.Errorf("%s: the READY rung records a build but invokes `erun build` nowhere, so it records a version it never minted", path)
			}
			for _, invocation := range invocations {
				if strings.Contains(invocation, "--release") {
					t.Errorf("%s: the READY rung builds with %q -- a release always targets every architecture and publishes, so every pull request pays for a two-architecture `-pr.<sha>` artifact set nothing consumes, and the project's configured platform narrowing is discarded because a release never consults it; READY asserts the commit builds, so this rung takes the plain build", path, strings.TrimSpace(invocation))
				}
			}
		}
	}
}

func TestMergeSkillReadyRungResolvesAFailedBuildsVersionWithoutBuilding(t *testing.T) {
	for _, path := range mergeReadyRungSkillPaths {
		for _, block := range mergeReadyRungBlocks(t, path) {
			// A plain build mints its version from a second-resolution snapshot
			// timestamp while building, so a run that failed before printing
			// its result leaves no JSON to re-read. The failure path has to
			// mint a same-form version from the repo state instead; a release
			// could resolve one up front, which is the only reason the failure
			// path ever worked, and it is gone with the release.
			if !strings.Contains(block, "--dry-run") {
				t.Errorf("%s: the READY rung's failure path resolves no version from `erun build --dry-run`, so a failed build has no version to record and the review cannot leave OPEN", path)
			}
			if strings.Contains(shellCommentLinePatrn.ReplaceAllString(block, ""), "--dry-run") {
				continue
			}
			t.Errorf("%s: the READY rung only mentions `--dry-run` in a comment, so the failure path does not actually resolve a version", path)
		}
	}
}
