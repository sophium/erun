package eruncommon

import (
	"reflect"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// repoRootForDockerPlatformsProjectConfigTest returns the repo root.
func repoRootForDockerPlatformsProjectConfigTest(t *testing.T) string {
	t.Helper()
	return erunRepoRoot(t)
}

// TestProjectConfigPinsAmd64OnlyPlatformsForConfirmedAmd64Environments locks in
// that a non-release build here stays amd64-only: it publishes nothing, so
// an environment whose node is confirmed x86_64 also building linux/arm64 under
// qemu emulation would buy an artifact nobody consumes.
//
// The agent environments inherit the pin from the project-wide docker.platforms
// default rather than each naming it, so this asserts the property per
// environment: a platform pin that only covers the environments someone
// remembered to list is the bug this shape exists to prevent.
//
// "code2" is the environment a live build was confirmed in (by probing with a
// bogus platform value and observing the resulting build trace), and its node
// was independently confirmed x86_64 (uname -m, docker info, docker buildx
// inspect).
//
// This reads the repo's own checked-in .erun/config.yaml at run time, which Go's
// test cache cannot see as a dependency of this package's compiled inputs — an
// edit to that YAML file alone (no .go change) can replay a stale cached PASS.
// Run with -count=1 to force a fresh read after editing .erun/config.yaml.
func TestProjectConfigPinsAmd64OnlyPlatformsForConfirmedAmd64Environments(t *testing.T) {
	repoRoot := repoRootForDockerPlatformsProjectConfigTest(t)

	cfg, _, err := config.LoadProjectConfig(repoRoot)
	if err != nil {
		t.Fatalf("LoadProjectConfig(%q) failed: %v", repoRoot, err)
	}

	want := []string{"linux/amd64"}
	for _, environment := range []string{"code1", "code2", "code3", "code4"} {
		if got := cfg.DockerPlatformsForEnvironment(environment); !reflect.DeepEqual(got, want) {
			t.Errorf("DockerPlatformsForEnvironment(%q) = %v, want %v (every agent environment on this x86_64 node should build amd64 only, so a non-release build there stops paying for emulated arm64)", environment, got, want)
		}
	}

	// "local" is the generic default name `erun init` assigns when none is
	// given (DefaultEnvironment), so it can belong to a contributor's own
	// machine of any architecture, including arm64. It must stay unpinned in
	// this shared, checked-in config — pinning it amd64-only here would
	// misdirect or silently narrow builds for whoever's "local" isn't amd64.
	if got := cfg.DockerPlatformsForEnvironment("local"); len(got) != 0 {
		t.Errorf(`DockerPlatformsForEnvironment("local") = %v, want empty: "local" is a generic name that can belong to a non-amd64 machine, so this shared config must not pin it`, got)
	}
	if got, want := resolveDockerBuildPlatformsForTest(t, repoRoot, "local"), shared.MultiPlatformDockerBuilds; !reflect.DeepEqual(got, want) {
		t.Errorf(`resolved platforms for "local" = %v, want %v: the project-wide amd64 pin must not reach the generic default environment`, got, want)
	}
}

func resolveDockerBuildPlatformsForTest(t *testing.T, projectRoot, environment string, override ...string) []string {
	t.Helper()
	platforms, err := shared.ResolveDockerBuildPlatforms(core.Context{}, projectRoot, environment, override)
	if err != nil {
		t.Fatalf("resolveDockerBuildPlatforms(%q, %q) failed: %v", projectRoot, environment, err)
	}
	return platforms
}
