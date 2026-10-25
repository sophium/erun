package settings

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
)

func TestPullCoordinatesRoundTripThroughTheEditor(t *testing.T) {
	existing := config.EnvConfig{
		RuntimeRegistry:  "old.example.com",
		ImagePullSecrets: []string{"old-secret"},
	}

	saved := environmentConfigFromUI(EnvironmentConfig{
		RuntimeRegistry:  "  ghcr.io/sophium  ",
		ImagePullSecrets: []string{"ecr-pull", "", "  ghcr-pull  "},
	}, existing)

	if saved.RuntimeRegistry != "ghcr.io/sophium" {
		t.Fatalf("runtime registry: got %q", saved.RuntimeRegistry)
	}
	if len(saved.ImagePullSecrets) != 2 ||
		saved.ImagePullSecrets[0] != "ecr-pull" ||
		saved.ImagePullSecrets[1] != "ghcr-pull" {
		t.Fatalf("pull secrets: got %v", saved.ImagePullSecrets)
	}
}

func TestClearingPullCoordinatesClearsTheConfig(t *testing.T) {
	existing := config.EnvConfig{
		RuntimeRegistry:  "old.example.com",
		ImagePullSecrets: []string{"old-secret"},
	}

	saved := environmentConfigFromUI(EnvironmentConfig{}, existing)

	if saved.RuntimeRegistry != "" {
		t.Fatalf("expected the runtime registry cleared, got %q", saved.RuntimeRegistry)
	}
	if saved.ImagePullSecrets != nil {
		t.Fatalf("expected the pull secrets cleared, got %v", saved.ImagePullSecrets)
	}
}
