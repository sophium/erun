package eruncommon

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/helm"
)

// VerifyPublishedDockerImage re-resolves the multi-arch manifest just pushed for
// a tag. Nothing else proves the image half of a version exists, and a release
// that assumed it does is how a tag gets announced for an image no deploy can
// pull.
func VerifyPublishedDockerImage(ctx core.Context, tag string, insecure bool) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return nil
	}
	spec := helm.CommandSpec{Name: "docker", Args: append(dockerManifestArgs("inspect", insecure), tag)}
	ctx.TraceCommand(spec.Dir, spec.Name, spec.Args...)
	if ctx.DryRun {
		return nil
	}
	if err := helm.ReadBackPublishedArtifact(ctx, "image "+tag, func() (string, error) {
		return runCommandCapturingOutput(ctx, spec)
	}); err != nil {
		return fmt.Errorf("verify published image %s: %w", tag, err)
	}
	ctx.Info("==> Verified published image " + tag)
	return nil
}

// runCommandCapturingOutput keeps a verification read quiet on success — the
// manifest body is not what the operator asked for — while still surfacing the
// tool's own diagnostics and capturing everything for failure classification.
// See commandOutputCapture for why stdout and stderr need separate buffers.
func runCommandCapturingOutput(ctx core.Context, spec helm.CommandSpec) (string, error) {
	capture := &core.CommandOutputCapture{}
	cmd := core.Command(spec.Name, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Stdout = &capture.Stdout
	cmd.Stderr = core.TeeWriter(ctx.Stderr, &capture.Stderr)
	err := cmd.Run()
	return capture.Combined(), err
}
