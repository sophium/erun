package mergequeue

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The erun-merge skill's OPEN -> READY shell block is the only driver of READY, so its
// build policy is real behavior: a plain build, never a release.
var mergeReadyRungSkillPaths = []string{
	"erun-skills/skills/erun-merge/SKILL.md",
}

var (
	fencedSHBlockPattern  = regexp.MustCompile("(?s)```sh\n(.*?)```")
	shellCommentLinePatrn = regexp.MustCompile(`(?m)^\s*#.*$`)
	erunBuildInvocation   = regexp.MustCompile(`erun build\b[^\n|]*`)
)

// mergeReadyRungBlocks fails on an empty result, which would satisfy the other assertions vacuously.
func mergeReadyRungBlocks(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(erunRepoRoot(t), path))
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

// buildInvocations drops `#` comment lines so prose does not read as an invocation.
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
			// A plain build that failed leaves no JSON, so the failure path must mint a version itself.
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
