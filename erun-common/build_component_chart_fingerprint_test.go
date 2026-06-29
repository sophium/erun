package eruncommon

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBuildFingerprintExcludesChartForVersionPinnedBase locks in the contract
// that a version-pinned base image (one carrying its own VERSION file in its
// build dir, e.g. erun-backend-postgres:18.3, erun-powerdns:4.9.3) does not fold
// its co-located Helm chart into the build fingerprint, while a release-
// versioned component still does. A pinned base's chart tracks the release
// version and is not published in lockstep with the image (only the runtime
// devops chart is), so hashing it would churn a stable image's fingerprint every
// release and force a needless rebuild (#695).
func TestBuildFingerprintExcludesChartForVersionPinnedBase(t *testing.T) {
	root := t.TempDir()
	dockerDir := filepath.Join(root, "mod", "docker", "comp")
	chartDir := filepath.Join(root, "mod", "k8s", "comp")
	for _, d := range []string{dockerDir, chartDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dockerfile := filepath.Join(dockerDir, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	chartFile := filepath.Join(chartDir, "Chart.yaml")
	writeChart := func(version string) {
		if err := os.WriteFile(chartFile, []byte("apiVersion: v2\nname: comp\nversion: "+version+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	newSpec := func(pinned bool) DockerBuildSpec {
		return DockerBuildSpec{
			ContextDir:     dockerDir,
			DockerfilePath: dockerfile,
			Image:          DockerImageReference{ImageName: "comp", VersionFromBuildDir: pinned},
		}
	}
	fingerprint := func(pinned bool) string {
		digest, err := computeBuildFingerprint(newSpec(pinned))
		if err != nil {
			t.Fatalf("computeBuildFingerprint: %v", err)
		}
		return digest
	}

	// Sanity: a release-versioned component (not pinned) DOES fold the chart in,
	// so a chart edit moves its fingerprint.
	writeChart("1.0.0")
	releaseV1 := fingerprint(false)
	writeChart("2.0.0")
	releaseV2 := fingerprint(false)
	if releaseV1 == releaseV2 {
		t.Fatal("expected a chart change to move the fingerprint for a release-versioned component")
	}

	// Contract: a version-pinned base ignores the chart entirely, so the same
	// chart edit leaves its fingerprint unchanged.
	writeChart("1.0.0")
	pinnedV1 := fingerprint(true)
	writeChart("2.0.0")
	pinnedV2 := fingerprint(true)
	if pinnedV1 != pinnedV2 {
		t.Fatalf("version-pinned base fingerprint must ignore chart changes: %s != %s", pinnedV1, pinnedV2)
	}
}
