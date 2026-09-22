package eruncommon

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBuildFingerprintSurvivesAPodRoll pins the identity a build is cached
// under — the fingerprint, and the fp-<fingerprint> tag a promotion resolves —
// against the one input a pod roll actually changes.
//
// CgroupParent is derived from the pod's own hostname, so it is different on
// every pod an environment runs in, and absent entirely outside a pod. It
// places the build's RUN containers under this environment's CPU cap; it is not
// an input to the image they produce. If it reached the fingerprint, a deploy
// or a resize would move the identity, the fp-tagged image the environment
// already holds would stop matching, and the environment would rebuild from
// cold with its cache still full, which is the shape a rolled environment's
// slow first build has. The content-change control at the end keeps the
// assertion honest: an identity that never moved would satisfy the roll cases
// without pinning anything.
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

	spec := func(cgroupParent string) DockerBuildSpec {
		return DockerBuildSpec{
			ContextDir:     root,
			DockerfilePath: dockerfile,
			Image: DockerImageReference{
				Registry:  "ghcr.io/sophium",
				ImageName: "comp",
				Version:   "1.0.291-snapshot-20260921T193239Z",
				Tag:       "ghcr.io/sophium/comp:1.0.291-snapshot",
			},
			Platforms:    []string{"linux/amd64"},
			CgroupParent: cgroupParent,
		}
	}
	// The tag a promotion resolves, which is what a rolled pod has to arrive at
	// the same value of to reuse what the environment already built.
	cacheKey := func(build DockerBuildSpec) string {
		t.Helper()
		digest, err := computeBuildFingerprint(build)
		if err != nil {
			t.Fatalf("computeBuildFingerprint: %v", err)
		}
		return fingerprintTag(build.Image, digest, build.Platforms[0])
	}

	// Two cgroup parents, as two pods of one environment derive them: the
	// hostname is the pod's own name, so a roll changes it.
	beforeRoll := cacheKey(spec("/docker/erun-build-cpu-cap-erun-devops-86cc5c7658-kmtdv"))
	afterRoll := cacheKey(spec("/docker/erun-build-cpu-cap-erun-devops-7bd4d79cb6-trvpp"))
	if beforeRoll != afterRoll {
		t.Fatalf("a pod roll must not change the cache key: %s != %s", beforeRoll, afterRoll)
	}
	// A host build carries no cgroup parent at all, and must land on the same
	// identity: the value is placement, not identity, on both sides of a roll.
	if host := cacheKey(spec("")); host != beforeRoll {
		t.Fatalf("the cgroup parent must not reach the cache key: %s != %s", host, beforeRoll)
	}

	// Control: a real input change still moves the identity.
	if err := os.WriteFile(payload, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if changed := cacheKey(spec("/docker/erun-build-cpu-cap-erun-devops-7bd4d79cb6-trvpp")); changed == afterRoll {
		t.Fatal("expected a context change to move the cache key")
	}
}
