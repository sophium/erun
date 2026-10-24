package build

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

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
			want:        shared.MultiPlatformDockerBuilds,
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

	if got := resolveDockerBuildPlatformsForTest(t, projectRoot, "code1"); !reflect.DeepEqual(got, shared.MultiPlatformDockerBuilds) {
		t.Errorf("resolved platforms with no pin configured = %v, want %v", got, shared.MultiPlatformDockerBuilds)
	}

	pinnedRoot := writeDockerPlatformsProjectConfig(t, `docker:
    platforms:
        - linux/amd64
`)
	// A release resolves the full pair before config is consulted and passes it
	// as an explicit override, which must keep winning over any pin.
	if got := resolveDockerBuildPlatformsForTest(t, pinnedRoot, "code1", shared.MultiPlatformDockerBuilds...); !reflect.DeepEqual(got, shared.MultiPlatformDockerBuilds) {
		t.Errorf("resolved platforms for a release build = %v, want %v: a release publishes every platform erun supports regardless of any pin", got, shared.MultiPlatformDockerBuilds)
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

func writeDockerPlatformsProjectConfig(t *testing.T, contents string) string {
	t.Helper()
	projectRoot := t.TempDir()
	configDir := filepath.Join(projectRoot, config.ProjectConfigDir)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) failed: %v", configDir, err)
	}
	if err := os.WriteFile(filepath.Join(configDir, config.ConfigFile), []byte(contents), 0o644); err != nil {
		t.Fatalf("writing test project config failed: %v", err)
	}
	return projectRoot
}
