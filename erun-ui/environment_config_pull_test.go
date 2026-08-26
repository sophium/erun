package main

import (
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// Both fields are pull-time coordinates the Manage dialog now edits, so an
// edit has to survive the round trip rather than silently reverting on save.
func TestPullCoordinatesRoundTripThroughTheEditor(t *testing.T) {
	existing := eruncommon.EnvConfig{
		RuntimeRegistry:  "old.example.com",
		ImagePullSecrets: []string{"old-secret"},
	}

	saved := environmentConfigFromUI(uiEnvironmentConfig{
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

// Clearing the field must clear the config, not leave the previous value in
// place -- a stale pull secret is how an env keeps failing after the operator
// believes they fixed it.
func TestClearingPullCoordinatesClearsTheConfig(t *testing.T) {
	existing := eruncommon.EnvConfig{
		RuntimeRegistry:  "old.example.com",
		ImagePullSecrets: []string{"old-secret"},
	}

	saved := environmentConfigFromUI(uiEnvironmentConfig{}, existing)

	if saved.RuntimeRegistry != "" {
		t.Fatalf("expected the runtime registry cleared, got %q", saved.RuntimeRegistry)
	}
	if saved.ImagePullSecrets != nil {
		t.Fatalf("expected the pull secrets cleared, got %v", saved.ImagePullSecrets)
	}
}
