package release

import (
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// TraceReleaseUmbrella emits the markers the desktop parses for a standalone
// release; dry-run omits them to keep goldens stable.
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

// RunReleaseSpec marks a release in source control and never builds or
// publishes; a tag without artifacts is a dead, undeployable version.
func RunReleaseSpec(ctx core.Context, spec model.ReleaseSpec, runGit gitx.GitCommandRunnerFunc) error {
	return shared.RunReleaseSpec(ctx, spec, runGit, nil, nil)
}
