package eruncommon

import (
	"bytes"
	"fmt"
	"strings"
)

// desktopUICoveragePathPrefix is the subtree the merge-queue gate's own
// `erun build` cannot verify: the erun-devops test stage `make check` runs
// inside has no Wails/webkit toolchain, so it never executes
// erun-ui/playwright even though erun-ui/AGENTS.md makes that suite
// mandatory for every desktop change.
const desktopUICoveragePathPrefix = "erun-ui/"

// desktopUICoverageChanged reports whether changedPaths includes a desktop
// change the gate cannot verify. A path is exempt only when it is pure
// documentation (.md) — the same "zero observable surface" exemption
// erun-ui/AGENTS.md's own mandatory-Playwright-coverage rule already carries
// for renames, internal helper extraction, and documentation.
func desktopUICoverageChanged(changedPaths []string) bool {
	for _, path := range changedPaths {
		if !strings.HasPrefix(path, desktopUICoveragePathPrefix) {
			continue
		}
		if strings.HasSuffix(path, ".md") {
			continue
		}
		return true
	}
	return false
}

// ensureDesktopPlaywrightCoverage refuses a passing GATE build that touches
// erun-ui/** without an explicit attestation that erun-ui/playwright/run.sh
// was actually run against it. A green GATE build proves nothing about the
// desktop frontend on its own today (see desktopUICoveragePathPrefix); this
// is a fail-closed stopgap, kept until the suite can run inside the gate for
// real.
func ensureDesktopPlaywrightCoverage(changedPaths []string, verified bool) error {
	if verified || !desktopUICoverageChanged(changedPaths) {
		return nil
	}
	return fmt.Errorf(
		"refusing to record a passing GATE build: this merge changes erun-ui/** and the merge-queue gate's " +
			"`erun build` does not run the erun-ui/playwright suite (issue #1933), so a green GATE build proves " +
			"nothing about the desktop frontend — build erun-app and run `erun-ui/playwright/run.sh` against " +
			"this exact commit, then re-run `erun review record-build --gate` with --desktop-playwright-verified " +
			"once it passes",
	)
}

// checkDesktopPlaywrightCoverageForGate is RunReviewRecordBuild's fail-closed
// desktop-coverage preflight: only a successful GATE build is worth checking
// (a failure already produces the FAILED status this check would otherwise
// force), and an inconclusive changed-paths read proceeds rather than
// blocks, the same "known failure over invented behavior" posture
// ensureReleaseDiskHeadroom uses.
func checkDesktopPlaywrightCoverageForGate(ctx Context, params ReviewRecordBuildParams) error {
	if !params.Gate || !params.Successful {
		return nil
	}
	changedPaths, err := resolveGateSquashChangedPaths(params.Root)
	if err != nil {
		ctx.Trace("desktop Playwright coverage check inconclusive, proceeding without it: " + err.Error())
		return nil
	}
	return ensureDesktopPlaywrightCoverage(changedPaths, params.DesktopPlaywrightVerified)
}

// resolveGateSquashChangedPaths lists the paths a gate-merge's squash commit
// actually changed, by diffing it against its own single parent — the
// target's pre-squash tip for a single-source gate (GateMergeWorkingTree's
// `git merge --squash` + `git commit` never produces a two-parent merge
// commit). A batched gate-merge (more than one landed source) lands a stack
// of these commits, so HEAD^..HEAD only sees the last-landed source's own
// diff, not the whole batch's — a real gap, but one that stays latent until
// a caller reports a GATE build against a multi-source batch's tip, which
// nothing does yet: batch-reporting for record-build/acceptMerged is exactly
// the "one-review-per-build off a shared batch build" question
// erun-backend-api/AGENTS.md § "Merge Queue" records as unresolved. An
// inconclusive read (no root, not a git repo, no parent commit) is reported
// as an error rather than an empty result, so the caller can choose to
// proceed rather than mistake "could not tell" for "nothing changed".
func resolveGateSquashChangedPaths(root string) ([]string, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("no project root resolved")
	}
	var stdout, stderr bytes.Buffer
	if err := GitCommandRunner(root, &stdout, &stderr, "diff", "--name-only", "HEAD^", "HEAD"); err != nil {
		return nil, fmt.Errorf("git diff HEAD^ HEAD: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var paths []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			paths = append(paths, line)
		}
	}
	return paths, nil
}
