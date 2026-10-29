package fingerprint

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/seam/imagebuild"
)

// A pin that no longer matches its Dockerfile never promotes, so every environment silently
// rebuilds the base from source. Reads .erun/config.yaml and sibling Dockerfiles: run with -count=1.
func TestConfiguredBaseFingerprintsMatchTheirDockerfiles(t *testing.T) {
	root := erunRepoRoot(t)
	cfg, _, err := config.LoadProjectConfig(root)
	if err != nil {
		t.Fatalf("load %s/.erun/config.yaml: %v", root, err)
	}
	environments := make([]string, 0, len(cfg.Environments))
	for name := range cfg.Environments {
		environments = append(environments, name)
	}
	sort.Strings(environments)
	checked := 0
	for _, environment := range environments {
		for image, pinned := range cfg.DockerFingerprintsForEnvironment(environment) {
			spec := imagebuild.DockerBuildSpec{
				ContextDir:     root,
				DockerfilePath: filepath.Join(root, "erun-devops", "docker", image, "Dockerfile"),
			}
			spec.Image.ImageName = image
			spec.Image.VersionFromBuildDir = true
			got, err := computeBuildFingerprint(spec)
			if err != nil {
				t.Errorf("environment %s: fingerprint %s: %v", environment, image, err)
				continue
			}
			checked++
			if got != pinned {
				t.Errorf("environment %s pins %s at %s but its Dockerfile fingerprints to %s: that pin never promotes, so the base rebuilds from source", environment, image, pinned, got)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no docker.fingerprints pin was checked; the config moved somewhere this test does not read")
	}
}
