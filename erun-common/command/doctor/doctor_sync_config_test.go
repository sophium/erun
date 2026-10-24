package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared/cloudconfig"
)

// syncConfigTestEnv returns a managed-cloud runtime pod's injected env with overrides applied.
func syncConfigTestEnv(overrides map[string]string) func(string) string {
	env := map[string]string{
		"ERUN_TENANT":               "team",
		"ERUN_ENVIRONMENT":          "prod",
		"ERUN_ENV_TYPE":             "runtime",
		"ERUN_REPO_REMOTE":          "true",
		"ERUN_CLOUD_PROVIDER":       "aws",
		"ERUN_CLOUD_PROVIDER_ALIAS": "ops+123456789012@aws",
		"ERUN_CLOUD_REGION":         "eu-west-2",
		"ERUN_KUBERNETES_CONTEXT":   "erun-001-team-eu-west-2",
	}
	for k, v := range overrides {
		env[k] = v
	}
	return func(key string) string { return env[key] }
}

func TestInjectedCloudContextNameDefaultsToKubernetesContext(t *testing.T) {
	injected, ok := cloudconfig.ResolveInjectedRuntimeConfig(syncConfigTestEnv(map[string]string{"ERUN_CLOUD_CONTEXT_NAME": ""}))
	if !ok {
		t.Fatalf("ResolveInjectedRuntimeConfig ok = false, want true")
	}
	if len(injected.Contexts) != 1 {
		t.Fatalf("len(Contexts) = %d, want 1", len(injected.Contexts))
	}
	if got, want := injected.Contexts[0].Name, "erun-001-team-eu-west-2"; got != want {
		t.Fatalf("Contexts[0].Name = %q, want %q (the kubernetes-context default)", got, want)
	}
}

func TestInjectedCloudContextNameExplicitUnchanged(t *testing.T) {
	injected, ok := cloudconfig.ResolveInjectedRuntimeConfig(syncConfigTestEnv(map[string]string{"ERUN_CLOUD_CONTEXT_NAME": "my-cloud-context"}))
	if !ok {
		t.Fatalf("ResolveInjectedRuntimeConfig ok = false, want true")
	}
	if len(injected.Contexts) != 1 {
		t.Fatalf("len(Contexts) = %d, want 1", len(injected.Contexts))
	}
	if got, want := injected.Contexts[0].Name, "my-cloud-context"; got != want {
		t.Fatalf("Contexts[0].Name = %q, want %q", got, want)
	}
}

func TestSyncConfigSecondRunReachesInSync(t *testing.T) {
	configHome := t.TempDir()
	env := syncConfigTestEnv(map[string]string{"ERUN_CLOUD_CONTEXT_NAME": ""})

	first, err := InspectRuntimeConfigSync(configHome, env)
	mustNoErr(t, err, "first inspect")
	if first.InSync() {
		t.Fatalf("first run: InSync() = true, want false (nothing on disk yet)")
	}
	mustNoErr(t, RunRuntimeConfigSync(testTraceContext(false), first), "first sync")

	second, err := InspectRuntimeConfigSync(configHome, env)
	mustNoErr(t, err, "second inspect")
	if !second.InSync() {
		t.Fatalf("second run: InSync() = false, want true; drift = %+v", second.Drift)
	}
}

func TestSyncConfigExplicitContextNameReachesInSync(t *testing.T) {
	configHome := t.TempDir()
	env := syncConfigTestEnv(map[string]string{"ERUN_CLOUD_CONTEXT_NAME": "my-cloud-context"})

	first, err := InspectRuntimeConfigSync(configHome, env)
	mustNoErr(t, err, "first inspect")
	if first.InSync() {
		t.Fatalf("first run: InSync() = true, want false (nothing on disk yet)")
	}
	mustNoErr(t, RunRuntimeConfigSync(testTraceContext(false), first), "first sync")

	second, err := InspectRuntimeConfigSync(configHome, env)
	mustNoErr(t, err, "second inspect")
	if !second.InSync() {
		t.Fatalf("second run: InSync() = false, want true; drift = %+v", second.Drift)
	}
}

func TestSyncConfigGenuineCloudContextDriftStillReported(t *testing.T) {
	configHome := t.TempDir()
	env := syncConfigTestEnv(map[string]string{"ERUN_CLOUD_CONTEXT_NAME": ""})

	first, err := InspectRuntimeConfigSync(configHome, env)
	mustNoErr(t, err, "first inspect")
	mustNoErr(t, RunRuntimeConfigSync(testTraceContext(false), first), "first sync")

	movedEnv := syncConfigTestEnv(map[string]string{"ERUN_CLOUD_CONTEXT_NAME": "", "ERUN_CLOUD_REGION": "eu-west-1"})
	drifted, err := InspectRuntimeConfigSync(configHome, movedEnv)
	mustNoErr(t, err, "drifted inspect")
	if drifted.InSync() {
		t.Fatalf("InSync() = true, want false for a genuinely moved cloud-context region")
	}
	found := false
	for _, field := range drifted.Drift {
		if field.Scope == "root" && field.Key == "cloudcontexts" && field.Kind == model.ConfigDriftWrong {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a root/cloudcontexts drift of kind %q, got %+v", model.ConfigDriftWrong, drifted.Drift)
	}
}

// The config directory is made read-only so any rewrite, even byte-identical, fails.
func TestSyncConfigRootNotRewrittenWhenInSync(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced the same way on windows")
	}
	configHome := t.TempDir()
	env := syncConfigTestEnv(map[string]string{"ERUN_CLOUD_CONTEXT_NAME": ""})

	first, err := InspectRuntimeConfigSync(configHome, env)
	mustNoErr(t, err, "first inspect")
	mustNoErr(t, RunRuntimeConfigSync(testTraceContext(false), first), "first sync")

	rootPath := config.RuntimeRootConfigPath(configHome)
	rootDir := filepath.Dir(rootPath)
	before, err := os.ReadFile(rootPath)
	mustNoErr(t, err, "read root config after first sync")

	mustNoErr(t, os.Chmod(rootDir, 0o555), "make config dir read-only")
	t.Cleanup(func() { _ = os.Chmod(rootDir, 0o755) })

	second, err := InspectRuntimeConfigSync(configHome, env)
	mustNoErr(t, err, "second inspect")
	if !second.InSync() {
		t.Fatalf("second run: InSync() = false, want true; drift = %+v", second.Drift)
	}
	mustNoErr(t, RunRuntimeConfigSync(testTraceContext(false), second), "second sync on a read-only config dir")

	mustNoErr(t, os.Chmod(rootDir, 0o755), "restore config dir permissions")
	after, err := os.ReadFile(rootPath)
	mustNoErr(t, err, "read root config after second sync")
	if !bytes.Equal(before, after) {
		t.Fatalf("root config changed even though nothing differed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
