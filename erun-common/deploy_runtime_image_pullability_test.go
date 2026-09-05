package eruncommon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// runtimeImagePullTraceContext returns a Context whose Trace/Info output
// lands in a buffer a test can assert against, alongside the DryRun flag
// under test -- the same WithTraceSink shape as traceCapturingContext
// (cloud_aws_sdk_test.go), plus the DryRun parameter this package's dry-run
// branches need.
func runtimeImagePullTraceContext(dryRun bool) (Context, *bytes.Buffer) {
	var buf bytes.Buffer
	return Context{Logger: NewLogger(VerbosityInfo).WithTraceSink(&buf), DryRun: dryRun}, &buf
}

// failingProbe fails the test if the anonymous-pullability probe runs at all,
// for cases where a resolved credential or an absent/non-ghcr image override
// must short-circuit before ever reaching the network.
func failingProbe(t *testing.T) anonymousPullProbeFunc {
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

func TestEnsureRuntimeImagePullSecretNonGHCRRegistryIsNoOp(t *testing.T) {
	ctx, _ := runtimeImagePullTraceContext(false)
	spec := HelmDeploySpec{
		Tenant:         "frs",
		ImageOverrides: map[string]string{DevopsComponentName: "111122223333.dkr.ecr.us-east-1.amazonaws.com/frs-devops:1.0.86"},
	}
	if err := ensureRuntimeImagePullSecret(ctx, &spec, failingProbe(t)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spec.ImagePullSecrets) != 0 {
		t.Fatalf("ImagePullSecrets = %v, want none: a non-ghcr registry is outside this preflight's scope", spec.ImagePullSecrets)
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
		ImageOverrides: map[string]string{DevopsComponentName: "ghcr.io/sophium/frs-devops:1.0.86"},
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
		ImageOverrides:   map[string]string{DevopsComponentName: "ghcr.io/sophium/frs-devops:1.0.86"},
	}
	if err := ensureRuntimeImagePullSecret(ctx, &spec, failingProbe(t)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spec.ImagePullSecrets) != 1 {
		t.Fatalf("ImagePullSecrets = %v, want the single existing entry left untouched", spec.ImagePullSecrets)
	}
}

// TestEnsureRuntimeImagePullSecretPublicImageWithNoCredentialDeploysUnchanged
// is the "do not break the working case" invariant: an env that legitimately
// rides a public image with no credential configured must keep deploying
// with no new requirement.
func TestEnsureRuntimeImagePullSecretPublicImageWithNoCredentialDeploysUnchanged(t *testing.T) {
	useDockerConfigDir(t, t.TempDir())

	ctx, trace := runtimeImagePullTraceContext(false)
	spec := HelmDeploySpec{
		Tenant:         "frs",
		ImageOverrides: map[string]string{DevopsComponentName: "ghcr.io/sophium/frs-devops:1.0.86"},
	}
	probe := func(_ context.Context, _ *http.Client, repoPath, tag string) (bool, error) {
		if repoPath != "sophium/frs-devops" || tag != "1.0.86" {
			t.Fatalf("probe got (%q, %q), want (sophium/frs-devops, 1.0.86)", repoPath, tag)
		}
		return true, nil
	}
	if err := ensureRuntimeImagePullSecret(ctx, &spec, probe); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spec.ImagePullSecrets) != 0 {
		t.Fatalf("ImagePullSecrets = %v, want none: a confirmed-public image needs no secret", spec.ImagePullSecrets)
	}
	if !strings.Contains(trace.String(), "anonymously pullable") {
		t.Fatalf("trace missing the pullable decision:\n%s", trace.String())
	}
}

// TestEnsureRuntimeImagePullSecretRefusesPrivateImageWithNoCredential is the
// exact outage scenario this preflight exists for: refuse before the caller
// does anything that could recreate the running pod.
func TestEnsureRuntimeImagePullSecretRefusesPrivateImageWithNoCredential(t *testing.T) {
	useDockerConfigDir(t, t.TempDir())

	ctx, _ := runtimeImagePullTraceContext(false)
	spec := HelmDeploySpec{
		Tenant:         "frs",
		ImageOverrides: map[string]string{DevopsComponentName: "ghcr.io/sophium/frs-devops:1.0.86"},
	}
	probe := func(context.Context, *http.Client, string, string) (bool, error) { return false, nil }
	err := ensureRuntimeImagePullSecret(ctx, &spec, probe)
	if err == nil {
		t.Fatal("expected a refusal for a private image with no resolvable credential")
	}
	if !strings.Contains(err.Error(), "ghcr.io/sophium/frs-devops:1.0.86") {
		t.Fatalf("error must name the image: %v", err)
	}
	if len(spec.ImagePullSecrets) != 0 {
		t.Fatalf("ImagePullSecrets = %v, want none: a refusal must not have mutated the spec", spec.ImagePullSecrets)
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
		ImageOverrides: map[string]string{DevopsComponentName: "ghcr.io/sophium/frs-devops:1.0.86"},
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

// TestEnsureRuntimeImagePullSecretDryRunNeverProbes proves --dry-run makes no
// live network call (goldens must stay offline and deterministic) while still
// tracing that a real deploy would refuse if the image turns out private.
func TestEnsureRuntimeImagePullSecretDryRunNeverProbes(t *testing.T) {
	useDockerConfigDir(t, t.TempDir())

	ctx, trace := runtimeImagePullTraceContext(true)
	spec := HelmDeploySpec{
		Tenant:         "frs",
		ImageOverrides: map[string]string{DevopsComponentName: "ghcr.io/sophium/frs-devops:1.0.86"},
	}
	if err := ensureRuntimeImagePullSecret(ctx, &spec, failingProbe(t)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(spec.ImagePullSecrets) != 0 {
		t.Fatalf("ImagePullSecrets = %v, want none in dry-run with no resolvable credential", spec.ImagePullSecrets)
	}
	if !strings.Contains(trace.String(), "no resolvable ghcr.io credential") {
		t.Fatalf("trace missing the no-credential decision:\n%s", trace.String())
	}
	if !strings.Contains(trace.String(), "refuses before rollout") {
		t.Fatalf("trace missing the dry-run warning about a real deploy's refusal:\n%s", trace.String())
	}
}

// TestEnsureRuntimeImagePullSecretDryRunWithCredentialStillAttaches proves the
// credential-resolves branch is fully decided in dry-run (it needs no network
// call), so --dry-run's traced helm command actually shows the attached
// secret instead of staying silent about it.
func TestEnsureRuntimeImagePullSecretDryRunWithCredentialStillAttaches(t *testing.T) {
	dir := writeDockerConfig(t, fmt.Sprintf(`{"auths":{"ghcr.io":{"auth":%q}}}`, b64Auth("alice:s3cret")))
	useDockerConfigDir(t, dir)

	ctx, _ := runtimeImagePullTraceContext(true)
	spec := HelmDeploySpec{
		Tenant:         "frs",
		ImageOverrides: map[string]string{DevopsComponentName: "ghcr.io/sophium/frs-devops:1.0.86"},
	}
	if err := ensureRuntimeImagePullSecret(ctx, &spec, failingProbe(t)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := autoImagePullSecretName("frs")
	if len(spec.ImagePullSecrets) != 1 || spec.ImagePullSecrets[0] != want {
		t.Fatalf("ImagePullSecrets = %v, want [%s] even under dry-run", spec.ImagePullSecrets, want)
	}
}

func TestAutoImagePullSecretName(t *testing.T) {
	if got, want := autoImagePullSecretName("frs"), "frs-devops-image-pull"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestRunHelmDeployRefusesBeforeAnyMutationForPrivateRuntimeImage is the
// end-to-end version of the refusal test above: RunHelmDeploy must refuse
// before it ensures the namespace, applies any pre-rollout secret, or invokes
// the chart deployer -- the exact ordering that matters under the runtime
// chart's Recreate strategy.
func TestRunHelmDeployRefusesBeforeAnyMutationForPrivateRuntimeImage(t *testing.T) {
	useDockerConfigDir(t, t.TempDir())

	original := ensureDeployNamespace
	ensureDeployNamespace = func(string, string) error {
		t.Fatal("the namespace must not be ensured when the pull-secret preflight refuses")
		return nil
	}
	t.Cleanup(func() { ensureDeployNamespace = original })

	spec := HelmDeploySpec{
		ReleaseName:       "frs-devops",
		Tenant:            "frs",
		KubernetesContext: "in-cluster",
		Namespace:         "frs-build",
		ChartPath:         "oci://ghcr.io/sophium/charts/erun-devops",
		ImageOverrides:    map[string]string{DevopsComponentName: "ghcr.io/sophium/frs-devops:1.0.86"},
	}

	realProbe := deployRuntimeImagePullProbe
	deployRuntimeImagePullProbe = func(context.Context, *http.Client, string, string) (bool, error) { return false, nil }
	t.Cleanup(func() { deployRuntimeImagePullProbe = realProbe })

	deployerCalled := false
	err := RunHelmDeploy(Context{}, spec, func(HelmDeployParams) error {
		deployerCalled = true
		return nil
	})
	if err == nil {
		t.Fatal("expected RunHelmDeploy to refuse for a private runtime image with no resolvable credential")
	}
	if deployerCalled {
		t.Fatal("the chart deployer must not run when the pull-secret preflight refuses")
	}
}
