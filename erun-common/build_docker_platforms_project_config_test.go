package eruncommon

import (
	"os"
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

	cfg, _, err := LoadProjectConfig(repoRoot)
	if err != nil {
		t.Fatalf("LoadProjectConfig(%q) failed: %v", repoRoot, err)
	}

	want := []string{"linux/amd64"}
	for _, environment := range []string{"code1", "code2", "code3", "code4"} {
		if got := cfg.DockerPlatformsForEnvironment(environment); !reflect.DeepEqual(got, want) {
			t.Errorf("DockerPlatformsForEnvironment(%q) = %v, want %v (every agent environment on this x86_64 node should build amd64 only, so a non-release gate build there stops paying for emulated arm64)", environment, got, want)
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
	if got, want := resolveDockerBuildPlatformsForTest(t, repoRoot, "local"), multiPlatformDockerBuilds; !reflect.DeepEqual(got, want) {
		t.Errorf(`resolved platforms for "local" = %v, want %v: the project-wide amd64 pin must not reach the generic default environment`, got, want)
	}
}

// TestProjectDockerPlatformsDefaultCoversUnlistedEnvironments covers the
// inheritance rules on a config of its own, so they do not depend on which
// environments the checked-in config happens to name today: an environment
// nobody listed inherits the project default, an environment's own list wins,
// and an explicit empty list refuses the default.
func TestProjectDockerPlatformsDefaultCoversUnlistedEnvironments(t *testing.T) {
	projectRoot := writeDockerPlatformsProjectConfig(t, `docker:
    platforms:
        - linux/amd64
environments:
    overridden:
        docker:
            platforms:
                - linux/arm64
    optedout:
        docker:
            platforms: []
`)

	cases := []struct {
		name        string
		environment string
		want        []string
	}{
		{
			name:        "an environment with no entry inherits the project default",
			environment: "code5",
			want:        []string{"linux/amd64"},
		},
		{
			name:        "an environment's own list wins over the project default",
			environment: "overridden",
			want:        []string{"linux/arm64"},
		},
		{
			name:        "an explicit empty list opts the environment out",
			environment: "optedout",
			want:        multiPlatformDockerBuilds,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveDockerBuildPlatformsForTest(t, projectRoot, tc.environment); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("resolved platforms for %q = %v, want %v", tc.environment, got, tc.want)
			}
		})
	}
}

// TestProjectDockerPlatformsAbsentKeepsEveryPlatform guards the default: a
// project that configures no platform pin anywhere still builds every platform
// erun supports, and a release build does so even where a pin is configured.
func TestProjectDockerPlatformsAbsentKeepsEveryPlatform(t *testing.T) {
	projectRoot := writeDockerPlatformsProjectConfig(t, `environments:
    local:
        docker:
            fingerprints:
                erun-ubuntu: 43855fd29e16b9aa
`)

	if got := resolveDockerBuildPlatformsForTest(t, projectRoot, "code1"); !reflect.DeepEqual(got, multiPlatformDockerBuilds) {
		t.Errorf("resolved platforms with no pin configured = %v, want %v", got, multiPlatformDockerBuilds)
	}

	pinnedRoot := writeDockerPlatformsProjectConfig(t, `docker:
    platforms:
        - linux/amd64
`)
	// A release resolves the full pair before config is consulted and passes it
	// as an explicit override, which must keep winning over any pin.
	if got := resolveDockerBuildPlatformsForTest(t, pinnedRoot, "code1", multiPlatformDockerBuilds...); !reflect.DeepEqual(got, multiPlatformDockerBuilds) {
		t.Errorf("resolved platforms for a release build = %v, want %v: a release publishes every platform erun supports regardless of any pin", got, multiPlatformDockerBuilds)
	}
}

func resolveDockerBuildPlatformsForTest(t *testing.T, projectRoot, environment string, override ...string) []string {
	t.Helper()
	platforms, err := resolveDockerBuildPlatforms(Context{}, projectRoot, environment, override)
	if err != nil {
		t.Fatalf("resolveDockerBuildPlatforms(%q, %q) failed: %v", projectRoot, environment, err)
	}
	return platforms
}

func writeDockerPlatformsProjectConfig(t *testing.T, contents string) string {
	t.Helper()
	projectRoot := t.TempDir()
	configDir := filepath.Join(projectRoot, projectConfigDir)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) failed: %v", configDir, err)
	}
	if err := os.WriteFile(filepath.Join(configDir, configFile), []byte(contents), 0o644); err != nil {
		t.Fatalf("writing test project config failed: %v", err)
	}
	return projectRoot
}
