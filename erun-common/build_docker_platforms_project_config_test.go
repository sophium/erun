package eruncommon

import (
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// repoRootForDockerPlatformsProjectConfigTest returns the repo root. erun-common
// sits directly under the root, so the grandparent of this file is the root
// regardless of the test's working directory.
func repoRootForDockerPlatformsProjectConfigTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(file))
}

// TestProjectConfigPinsAmd64OnlyPlatformsForConfirmedAmd64Environments locks in
// that a non-release gate build here stays amd64-only: it publishes nothing, so
// an environment whose node is confirmed x86_64 also building linux/arm64 under
// qemu emulation would buy an artifact nobody consumes.
// "code2" is pinned because a live build in that environment was confirmed (by
// probing with a bogus platform value and observing the resulting build trace)
// to resolve its docker.platforms lookup to exactly that key, and its node was
// independently confirmed x86_64 (uname -m, docker info, docker buildx inspect).
//
// This reads the repo's own checked-in .erun/config.yaml at run time, which Go's
// test cache cannot see as a dependency of this package's compiled inputs — an
// edit to that YAML file alone (no .go change) can replay a stale cached PASS.
// Run with -count=1 to force a fresh read after editing .erun/config.yaml.
func TestProjectConfigPinsAmd64OnlyPlatformsForConfirmedAmd64Environments(t *testing.T) {
	repoRoot := repoRootForDockerPlatformsProjectConfigTest(t)

	cfg, _, err := LoadProjectConfig(repoRoot)
	if err != nil {
		t.Fatalf("LoadProjectConfig(%q) failed: %v", repoRoot, err)
	}

	got := cfg.DockerPlatformsForEnvironment("code2")
	want := []string{"linux/amd64"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("environments.code2.docker.platforms = %v, want %v (a confirmed-amd64 environment should stay pinned so non-release builds there stop paying for emulated arm64)", got, want)
	}

	// "local" is the generic default name `erun init` assigns when none is
	// given (DefaultEnvironment), so it can belong to a contributor's own
	// machine of any architecture, including arm64. It must stay unpinned in
	// this shared, checked-in config — pinning it amd64-only here would
	// misdirect or silently narrow builds for whoever's "local" isn't amd64.
	if got := cfg.DockerPlatformsForEnvironment("local"); len(got) != 0 {
		t.Errorf(`environments.local.docker.platforms = %v, want empty: "local" is a generic name that can belong to a non-amd64 machine, so this shared config must not pin it`, got)
	}
}
