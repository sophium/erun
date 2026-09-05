package eruncommon

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// syncConfigTestEnv returns the env lookup a runtime pod's chart-injected
// ERUN_* vars would populate for a managed-cloud runtime env, with any
// overrides layered on top -- e.g. {"ERUN_CLOUD_CONTEXT_NAME": ""} to model
// erun#1662's trigger: a cluster whose kube-context was never registered with
// `erun context init`, so the entrypoint leaves the context name unset while
// still resolving provider/alias/region from the AWS alias.
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

// TestInjectedCloudContextNameDefaultsToKubernetesContext pins the erun#1662
// fix: an unset ERUN_CLOUD_CONTEXT_NAME must default the injected context name
// the same way NormalizeCloudContextConfig -- the rule every writer already
// applies -- defaults it, so the injected projection is comparable against the
// on-disk config by construction instead of via a second, driftable copy of
// the rule.
func TestInjectedCloudContextNameDefaultsToKubernetesContext(t *testing.T) {
	injected, ok := ResolveInjectedRuntimeConfig(syncConfigTestEnv(map[string]string{"ERUN_CLOUD_CONTEXT_NAME": ""}))
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

// TestInjectedCloudContextNameExplicitUnchanged locks the unchanged side: a
// chart that does set ERUN_CLOUD_CONTEXT_NAME (a cluster whose context was
// registered via `erun context init`) must still see that exact name, not the
// kubernetes-context default.
func TestInjectedCloudContextNameExplicitUnchanged(t *testing.T) {
	injected, ok := ResolveInjectedRuntimeConfig(syncConfigTestEnv(map[string]string{"ERUN_CLOUD_CONTEXT_NAME": "my-cloud-context"}))
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

// TestSyncConfigSecondRunReachesInSync is the end-to-end proof that erun#1662's
// perpetual-drift loop is gone: with ERUN_CLOUD_CONTEXT_NAME unset, a first
// --sync-config run must reconcile the missing cloud context, and -- this is
// the assertion that actually proves the loop is broken, not just the first
// reconcile -- a second run against the files the first run wrote must report
// InSync().
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

// TestSyncConfigExplicitContextNameReachesInSync locks the unchanged side end
// to end: an explicitly named cloud context reconciles and round-trips to
// InSync() exactly like it always did.
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

// TestSyncConfigGenuineCloudContextDriftStillReported proves the fix does not
// silence a real disagreement: once the region on disk is stale, drift must
// still be reported even though the context name now matches by construction.
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
		if field.Scope == "root" && field.Key == "cloudcontexts" && field.Kind == ConfigDriftWrong {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a root/cloudcontexts drift of kind %q, got %+v", ConfigDriftWrong, drifted.Drift)
	}
}

// TestSyncConfigRootNotRewrittenWhenInSync proves RunRuntimeConfigSync never
// touches the on-disk root config once the two sides already agree. It makes
// the config directory read-only after the first reconcile, so any attempted
// rewrite -- even one that would produce byte-identical content -- fails
// loudly instead of passing silently, then confirms the file is untouched.
func TestSyncConfigRootNotRewrittenWhenInSync(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced the same way on windows")
	}
	configHome := t.TempDir()
	env := syncConfigTestEnv(map[string]string{"ERUN_CLOUD_CONTEXT_NAME": ""})

	first, err := InspectRuntimeConfigSync(configHome, env)
	mustNoErr(t, err, "first inspect")
	mustNoErr(t, RunRuntimeConfigSync(testTraceContext(false), first), "first sync")

	rootPath := runtimeRootConfigPath(configHome)
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
