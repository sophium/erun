package push

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/helm"
	"github.com/sophium/erun/erun-common/primitive/shared"
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
	spec := helm.CommandSpec{Name: "docker", Args: append(shared.DockerManifestArgs("inspect", insecure), tag)}
	ctx.TraceCommand(spec.Dir, spec.Name, spec.Args...)
	if ctx.DryRun {
		return nil
	}
	if err := helm.ReadBackPublishedArtifact(ctx, "image "+tag, func() (string, error) {
		return shared.RunCommandCapturingOutput(ctx, spec)
	}); err != nil {
		return fmt.Errorf("verify published image %s: %w", tag, err)
	}
	ctx.Info("==> Verified published image " + tag)
	return nil
}
