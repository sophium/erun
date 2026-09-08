package eruncommon

import (
	"os"
	"regexp"
	"sort"
	"strings"
)

// dockerfilePlaywrightAreasPattern matches the PLAYWRIGHT_TEST_AREAS ARG
// declaration (erun-devops/docker/erun-devops/Dockerfile and any tenant
// `-devops` Dockerfile derived from it). Only such a Dockerfile gets the
// matching --build-arg; every other build's docker command is unchanged --
// the same scoping shape dockerfileConsumesDindCPULimit uses.
var dockerfilePlaywrightAreasPattern = regexp.MustCompile(`(?m)^\s*ARG\s+PLAYWRIGHT_TEST_AREAS\b`)

func dockerfileConsumesPlaywrightTestAreas(dockerfilePath string) bool {
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return false
	}
	return dockerfilePlaywrightAreasPattern.Match(data)
}

// playwrightRoot / playwrightAreasRoot / playwrightSmokeRoot are relative to
// the repository root and mirror the layout erun-ui/playwright/AGENTS.md
// documents: tests/smoke/ always runs, tests/areas/<area>/ is what `erun
// build` selects between, and everything else under playwrightRoot -- the
// harness (run.sh), its dependency manifest (package.json), fixtures, page
// objects, global setup/teardown, playwright.config.ts, and anything else
// added there later -- is infrastructure the whole suite depends on. That is
// expressed as "not an area, not smoke" rather than an explicit list of
// shared paths, so a new harness file is shared infra by default instead of
// needing to be added to a list someone has to remember.
const (
	playwrightRoot      = "erun-ui/playwright/"
	playwrightAreasRoot = playwrightRoot + "tests/areas/"
	playwrightSmokeRoot = playwrightRoot + "tests/smoke/"
)

// applyPlaywrightAreaBuildArgs threads the smoke+area selection resolved from
// the Playwright spec-file diff against the merge base into
// build.PlaywrightTestAreas when the Dockerfile declares the matching ARG.
// Left empty (the Dockerfile's own ARG default, which runs the full suite) when
// the Dockerfile does not consume it, or when the selection could not be
// resolved -- a gap in the git history must cost time, never coverage.
func applyPlaywrightAreaBuildArgs(ctx Context, projectRoot string, build *DockerBuildSpec) {
	if !dockerfileConsumesPlaywrightTestAreas(build.DockerfilePath) {
		return
	}
	if selection, ok := resolvePlaywrightTestAreaSelection(ctx, projectRoot); ok {
		build.PlaywrightTestAreas = selection
	}
}

// resolvePlaywrightTestAreaSelection derives what `erun build` should run:
//
//   - a path under playwrightRoot changed that is not a tests/areas/<area>/
//     or tests/smoke/ spec (harness scripts like run.sh, package.json,
//     fixtures, pages, global-setup/teardown, playwright.config.ts, ...) ->
//     "all", the full suite
//   - no Playwright spec file changed at all -> "smoke"
//   - one or more tests/areas/<area>/ spec files changed -> "smoke,<area>,..."
//
// Returns ok=false when the selection cannot be resolved (not a git
// repository, or no merge base against any candidate upstream branch) so the
// caller leaves the Dockerfile's own ARG default in place -- always the full
// suite, never zero coverage, per the issue's fail-safe direction.
func resolvePlaywrightTestAreaSelection(ctx Context, projectRoot string) (string, bool) {
	mergeBase, ok := resolvePlaywrightMergeBase(ctx, projectRoot)
	if !ok {
		return "", false
	}

	changed, err := playwrightChangedFiles(ctx, projectRoot, mergeBase)
	if err != nil {
		return "", false
	}

	return classifyPlaywrightChangedFiles(changed), true
}

// classifyPlaywrightChangedFiles turns a repo-root-relative changed-file list
// into "all" / "smoke" / "smoke,<area>,...", split out from
// resolvePlaywrightTestAreaSelection so each function stays under the
// project's cyclomatic-complexity ceiling.
func classifyPlaywrightChangedFiles(changed []string) string {
	areas := map[string]bool{}
	for _, file := range changed {
		if playwrightChangeAffectsEveryArea(file) {
			return "all"
		}
		if rest, ok := strings.CutPrefix(file, playwrightAreasRoot); ok {
			if area, _, found := strings.Cut(rest, "/"); found && area != "" {
				areas[area] = true
			}
		}
	}

	if len(areas) == 0 {
		return "smoke"
	}
	sorted := make([]string, 0, len(areas))
	for area := range areas {
		sorted = append(sorted, area)
	}
	sort.Strings(sorted)
	return "smoke," + strings.Join(sorted, ",")
}

// playwrightChangeAffectsEveryArea decides "all" for a change under
// playwrightRoot that is neither a tests/areas/<area>/ spec (which narrows to
// that area) nor a tests/smoke/ spec (which needs no broadening: smoke
// already runs in every selection). A file outside playwrightRoot entirely
// is not this function's concern -- it falls through to the caller's
// "smoke"-only default.
//
// Markdown is carved out deliberately, not by oversight: a `.md` file such as
// erun-ui/playwright/AGENTS.md is read by humans and agents, never by the
// harness or a spec, so it cannot break a test. Buying it the ~21-minute full
// suite would cost real gate time for a change with zero ability to regress
// anything the suite checks.
func playwrightChangeAffectsEveryArea(file string) bool {
	rest, ok := strings.CutPrefix(file, playwrightRoot)
	if !ok {
		return false
	}
	if strings.HasPrefix(file, playwrightAreasRoot) || strings.HasPrefix(file, playwrightSmokeRoot) {
		return false
	}
	return !strings.HasSuffix(rest, ".md")
}

// playwrightMergeBaseCandidates mirrors resolveGitDiffReviewBase's own branch
// list (diff.go): the first of these that resolves a merge-base with HEAD is
// the base a change is measured against, in priority order.
var playwrightMergeBaseCandidates = []string{"origin/HEAD", "origin/main", "origin/develop", "main", "develop"}

func resolvePlaywrightMergeBase(ctx Context, projectRoot string) (string, bool) {
	for _, branch := range playwrightMergeBaseCandidates {
		if base, ok := gitMergeBase(ctx, projectRoot, "HEAD", branch); ok {
			return base, true
		}
	}
	return "", false
}

// playwrightChangedFiles unions three views of "what changed" so an
// uncommitted or untracked spec file (the common case while iterating on a
// build locally) is not missed: the committed diff since the merge base, the
// uncommitted diff against HEAD (staged and unstaged), and untracked new
// files. Paths are repo-root-relative, matching git's own --name-only output.
func playwrightChangedFiles(ctx Context, projectRoot, mergeBase string) ([]string, error) {
	seen := map[string]bool{}
	var files []string
	add := func(output string) {
		for _, line := range strings.Split(output, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || seen[line] {
				continue
			}
			seen[line] = true
			files = append(files, line)
		}
	}

	committed, err := gitNameOnly(ctx, projectRoot, "diff", "--no-color", "--name-only", mergeBase+"...HEAD")
	if err != nil {
		return nil, err
	}
	add(committed)

	uncommitted, err := gitNameOnly(ctx, projectRoot, "diff", "--no-color", "--name-only", "HEAD")
	if err != nil {
		return nil, err
	}
	add(uncommitted)

	untracked, err := gitNameOnly(ctx, projectRoot, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	add(untracked)

	return files, nil
}

// gitNameOnly runs a git subcommand expected to print one repo-root-relative
// path per line, traced the same way gitMergeBase (work_clone_reclaim.go)
// traces its own git invocations.
func gitNameOnly(ctx Context, dir string, args ...string) (string, error) {
	fullArgs := append([]string{"-C", dir}, args...)
	ctx.TraceCommand("", "git", fullArgs...)
	output, err := Command("git", fullArgs...).Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}
