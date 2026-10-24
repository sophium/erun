package release

import (
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// TraceReleaseUmbrella brackets a standalone `erun release` with the
// `==> Releasing`/`==> Released`/`==> Release failed` lines the desktop
// activity-queue parser keys off to light the sidebar spinner, and starts the
// step-timing root reported (as a duration-ordered table plus a JSON record)
// when the bracket closes. `erun build --release` runs the same work under
// its own `==> Building` umbrella instead of opening a second entry; dry-run
// omits the markers and the timing so the release goldens stay stable.
func TraceReleaseUmbrella(ctx core.Context, version string) (core.Context, func(*error)) {
	if ctx.DryRun {
		return ctx, func(*error) {}
	}
	releasing, released := "==> Releasing", "==> Released"
	if target := strings.TrimSpace(version); target != "" {
		releasing += " " + target
		released += " " + target
	}
	started := time.Now()
	ctx.Info(releasing)
	root := core.NewStepTiming("release", nil)
	ctx.Timing = root
	return ctx, func(errp *error) {
		var err error
		if errp != nil {
			err = *errp
		}
		root.Finish(err)
		elapsed := time.Since(started).Round(time.Second)
		if err != nil {
			ctx.Info("==> Release failed after " + elapsed.String())
		} else {
			ctx.Info(released + " in " + elapsed.String())
		}
		core.ReportStepTiming(ctx, "release", root)
	}
}

// RunReleaseSpec marks a release in source control and nothing else. It
// resolves the version, stamps it into charts and package-manager metadata,
// commits it, tags it locally, pushes the tag and branches, syncs packaging
// checksums, and prepares the next patch -- and it never builds, publishes, or
// verifies an artifact.
//
// Build and publish are a different concern with different owners:
// `erun build --release` composes this same stamp/tag work with the build and
// the push, and `erun push --version <v>` republishes what a build produced.
// So this exits 0 having published nothing, which is not a failure and not
// corruption: a tag whose artifacts never landed names a dead version, and
// because `erun deploy` never builds, a dead version is not deployable by
// accident. If the release's source is wrong, fix it and release again.
func RunReleaseSpec(ctx core.Context, spec shared.ReleaseSpec, runGit gitx.GitCommandRunnerFunc) error {
	return shared.RunReleaseSpec(ctx, spec, runGit, nil, nil)
}
