package run

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam"
)

func runtimeImagePullTraceContext(dryRun bool) (core.Context, *bytes.Buffer) {
	var buf bytes.Buffer
	return core.Context{Logger: core.NewLogger(core.VerbosityInfo).WithTraceSink(&buf), DryRun: dryRun}, &buf
}

func TestEnsureRuntimeImagePullSecretRefusesOnInconclusiveProbe(t *testing.T) {
	useDockerConfigDir(t, t.TempDir())

	ctx, _ := runtimeImagePullTraceContext(false)
	spec := seam.HelmDeploySpec{
		Tenant:         "frs",
		ImageOverrides: map[string]string{config.DevopsComponentName: "ghcr.io/sophium/frs-devops:1.0.86"},
	}
	probeErr := errors.New("dial tcp ghcr.io: connection refused")
	probe := func(context.Context, *http.Client, string, string) (bool, error) { return false, probeErr }
	err := ensureRuntimeImagePullSecret(ctx, &spec, probe)
	if err == nil {
		t.Fatal("expected a refusal when pullability cannot be determined")
	}
	if !errors.Is(err, probeErr) {
		t.Fatalf("error must wrap the underlying probe failure: %v", err)
	}
	if len(spec.ImagePullSecrets) != 0 {
		t.Fatalf("ImagePullSecrets = %v, want none: an inconclusive probe must not have mutated the spec", spec.ImagePullSecrets)
	}
}
