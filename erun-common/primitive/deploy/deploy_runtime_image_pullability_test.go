package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// runtimeImagePullTraceContext returns a Context whose Trace/Info output
// lands in a buffer a test can assert against, alongside the DryRun flag
// under test -- the same WithTraceSink shape as traceCapturingContext
// (cloud_aws_sdk_test.go), plus the DryRun parameter this package's dry-run
// branches need.
func runtimeImagePullTraceContext(dryRun bool) (core.Context, *bytes.Buffer) {
	var buf bytes.Buffer
	return core.Context{Logger: core.NewLogger(core.VerbosityInfo).WithTraceSink(&buf), DryRun: dryRun}, &buf
}

// failingProbe fails the test if the anonymous-pullability probe runs at all,
// for cases where a resolved credential or an absent/non-ghcr image override
// must short-circuit before ever reaching the network.
func failingProbe(t *testing.T) shared.AnonymousPullProbeFunc {
	t.Helper()
	return func(context.Context, *http.Client, string, string) (bool, error) {
		t.Fatal("anonymous pullability probe must not run here")
		return false, nil
	}
}

func TestEnsureRuntimeImagePullSecretNoImageOverrideIsNoOp(t *testing.T) {
	ctx, _ := runtimeImagePullTraceContext(false)
	spec := HelmDeploySpec{Tenant: "frs"}
	if err := ensureRuntimeImagePullSecret(ctx, &spec, failingProbe(t)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spec.ImagePullSecrets) != 0 {
		t.Fatalf("ImagePullSecrets = %v, want none for a deploy with no runtime image override", spec.ImagePullSecrets)
	}
}

// TestEnsureRuntimeImagePullSecretAttachesWhenCredentialResolves proves the
// core fix: a resolvable ghcr.io credential auto-provisions and attaches a
// pull secret without needing an operator to have named one, and never
// touches the network to decide whether the image is actually private.
func TestEnsureRuntimeImagePullSecretAttachesWhenCredentialResolves(t *testing.T) {
	dir := writeDockerConfig(t, fmt.Sprintf(`{"auths":{"ghcr.io":{"auth":%q}}}`, b64Auth("alice:s3cret")))
	useDockerConfigDir(t, dir)

	ctx, trace := runtimeImagePullTraceContext(false)
	spec := HelmDeploySpec{
		Tenant:         "frs",
		ImageOverrides: map[string]string{config.DevopsComponentName: "ghcr.io/sophium/frs-devops:1.0.86"},
	}
	if err := ensureRuntimeImagePullSecret(ctx, &spec, failingProbe(t)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := autoImagePullSecretName("frs")
	if len(spec.ImagePullSecrets) != 1 || spec.ImagePullSecrets[0] != want {
		t.Fatalf("ImagePullSecrets = %v, want [%s]", spec.ImagePullSecrets, want)
	}
	if !strings.Contains(trace.String(), want) {
		t.Fatalf("trace missing the attached secret name %q:\n%s", want, trace.String())
	}
}

// TestEnsureRuntimeImagePullSecretDoesNotDuplicateAnExistingEntry proves a
// redeploy (or an operator who already named the same auto secret by
// coincidence) never grows the list on every run.
func TestEnsureRuntimeImagePullSecretDoesNotDuplicateAnExistingEntry(t *testing.T) {
	dir := writeDockerConfig(t, fmt.Sprintf(`{"auths":{"ghcr.io":{"auth":%q}}}`, b64Auth("alice:s3cret")))
	useDockerConfigDir(t, dir)

	ctx, _ := runtimeImagePullTraceContext(false)
	name := autoImagePullSecretName("frs")
	spec := HelmDeploySpec{
		Tenant:           "frs",
		ImagePullSecrets: []string{name},
		ImageOverrides:   map[string]string{config.DevopsComponentName: "ghcr.io/sophium/frs-devops:1.0.86"},
	}
	if err := ensureRuntimeImagePullSecret(ctx, &spec, failingProbe(t)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spec.ImagePullSecrets) != 1 {
		t.Fatalf("ImagePullSecrets = %v, want the single existing entry left untouched", spec.ImagePullSecrets)
	}
}

// TestEnsureRuntimeImagePullSecretRefusesOnInconclusiveProbe proves an
// inconclusive pullability probe is never treated as "public" -- exactly the
// assumption that let this failure hide until the image actually went
// private.
func TestEnsureRuntimeImagePullSecretRefusesOnInconclusiveProbe(t *testing.T) {
	useDockerConfigDir(t, t.TempDir())

	ctx, _ := runtimeImagePullTraceContext(false)
	spec := HelmDeploySpec{
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
