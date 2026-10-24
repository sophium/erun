package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// CgroupParent derives from the pod hostname and is placement, not image identity, so a
// pod roll must not move the fingerprint.
func TestBuildFingerprintSurvivesAPodRoll(t *testing.T) {
	root := t.TempDir()
	dockerDir := filepath.Join(root, "mod", "docker", "comp")
	if err := os.MkdirAll(dockerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dockerfile := filepath.Join(dockerDir, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM scratch\nCOPY payload.txt /payload\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(root, "payload.txt")
	if err := os.WriteFile(payload, []byte("payload\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	spec := func(cgroupParent string) shared.DockerBuildSpec {
		return shared.DockerBuildSpec{
			ContextDir:     root,
			DockerfilePath: dockerfile,
			Image: model.DockerImageReference{
				Registry:  "ghcr.io/sophium",
				ImageName: "comp",
				Version:   "1.0.291-snapshot-20260921T193239Z",
				Tag:       "ghcr.io/sophium/comp:1.0.291-snapshot",
			},
			Platforms:    []string{"linux/amd64"},
			CgroupParent: cgroupParent,
		}
	}
	cacheKey := func(build shared.DockerBuildSpec) string {
		t.Helper()
		digest, err := computeBuildFingerprint(build)
		if err != nil {
			t.Fatalf("computeBuildFingerprint: %v", err)
		}
		return shared.FingerprintTag(build.Image, digest, build.Platforms[0])
	}

	beforeRoll := cacheKey(spec("/docker/erun-build-cpu-cap-erun-devops-86cc5c7658-kmtdv"))
	afterRoll := cacheKey(spec("/docker/erun-build-cpu-cap-erun-devops-7bd4d79cb6-trvpp"))
	if beforeRoll != afterRoll {
		t.Fatalf("a pod roll must not change the cache key: %s != %s", beforeRoll, afterRoll)
	}
	if host := cacheKey(spec("")); host != beforeRoll {
		t.Fatalf("the cgroup parent must not reach the cache key: %s != %s", host, beforeRoll)
	}

	if err := os.WriteFile(payload, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed := cacheKey(spec("/docker/erun-build-cpu-cap-erun-devops-7bd4d79cb6-trvpp")); changed == afterRoll {
		t.Fatal("expected a context change to move the cache key")
	}
}
