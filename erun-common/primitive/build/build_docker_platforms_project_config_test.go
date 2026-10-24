package build

import (
	"reflect"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerbuild"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

func resolveDockerBuildPlatformsForTest(t *testing.T, projectRoot, environment string, override ...string) []string {
	t.Helper()
	platforms, err := dockerbuild.ResolveDockerBuildPlatforms(core.Context{}, projectRoot, environment, override)
	if err != nil {
		t.Fatalf("resolveDockerBuildPlatforms(%q, %q) failed: %v", projectRoot, environment, err)
	}
	return platforms
}

func repoRootForDockerPlatformsProjectConfigTest(t *testing.T) string {
	t.Helper()
	return erunRepoRoot(t)
}

// Reads the checked-in .erun/config.yaml, which the test cache does not track: use -count=1 after editing it.
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

	// "local" is `erun init`'s default name and may be any architecture, so it stays unpinned.
	if got := cfg.DockerPlatformsForEnvironment("local"); len(got) != 0 {
		t.Errorf(`DockerPlatformsForEnvironment("local") = %v, want empty: "local" is a generic name that can belong to a non-amd64 machine, so this shared config must not pin it`, got)
	}
	if got, want := resolveDockerBuildPlatformsForTest(t, repoRoot, "local"), dockerspec.MultiPlatformDockerBuilds; !reflect.DeepEqual(got, want) {
		t.Errorf(`resolved platforms for "local" = %v, want %v: the project-wide amd64 pin must not reach the generic default environment`, got, want)
	}
}
