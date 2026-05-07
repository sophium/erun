package eruncommon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestComputeBuildFingerprintStableAcrossNoOpRebuilds(t *testing.T) {
	contextDir := t.TempDir()
	dockerfilePath := filepath.Join(contextDir, "Dockerfile")
	requireNoError(t, os.WriteFile(dockerfilePath, []byte("FROM scratch\nCOPY app.go /app.go\n"), 0o644), "write Dockerfile")
	requireNoError(t, os.WriteFile(filepath.Join(contextDir, "app.go"), []byte("package main\n"), 0o644), "write app.go")

	build := DockerBuildSpec{
		ContextDir:     contextDir,
		DockerfilePath: dockerfilePath,
		Image:          DockerImageReference{Tag: "ghcr.io/test/app:1.0.0", Registry: "ghcr.io/test", ImageName: "app"},
	}

	first, err := computeBuildFingerprint(build)
	requireNoError(t, err, "computeBuildFingerprint first")
	second, err := computeBuildFingerprint(build)
	requireNoError(t, err, "computeBuildFingerprint second")
	if first != second {
		t.Fatalf("fingerprint must be stable: %q vs %q", first, second)
	}
}

func TestComputeBuildFingerprintChangesWhenCopiedFileChanges(t *testing.T) {
	contextDir := t.TempDir()
	dockerfilePath := filepath.Join(contextDir, "Dockerfile")
	requireNoError(t, os.WriteFile(dockerfilePath, []byte("FROM scratch\nCOPY app.go /app.go\n"), 0o644), "write Dockerfile")
	requireNoError(t, os.WriteFile(filepath.Join(contextDir, "app.go"), []byte("package main\n"), 0o644), "write app.go")

	build := DockerBuildSpec{
		ContextDir:     contextDir,
		DockerfilePath: dockerfilePath,
		Image:          DockerImageReference{Tag: "ghcr.io/test/app:1.0.0", Registry: "ghcr.io/test", ImageName: "app"},
	}

	before, err := computeBuildFingerprint(build)
	requireNoError(t, err, "computeBuildFingerprint before")

	requireNoError(t, os.WriteFile(filepath.Join(contextDir, "app.go"), []byte("package main\n// changed\n"), 0o644), "modify app.go")
	after, err := computeBuildFingerprint(build)
	requireNoError(t, err, "computeBuildFingerprint after")

	if before == after {
		t.Fatalf("fingerprint must change when COPY source content changes: %q", before)
	}
}

func TestComputeBuildFingerprintHonorsDockerignore(t *testing.T) {
	contextDir := t.TempDir()
	dockerfilePath := filepath.Join(contextDir, "Dockerfile")
	requireNoError(t, os.WriteFile(dockerfilePath, []byte("FROM scratch\nCOPY src /src\n"), 0o644), "write Dockerfile")
	srcDir := filepath.Join(contextDir, "src")
	requireNoError(t, os.MkdirAll(srcDir, 0o755), "mkdir src")
	requireNoError(t, os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package main\n"), 0o644), "write main.go")
	requireNoError(t, os.WriteFile(filepath.Join(srcDir, "ignored.txt"), []byte("noise\n"), 0o644), "write ignored.txt")
	requireNoError(t, os.WriteFile(filepath.Join(contextDir, ".dockerignore"), []byte("src/ignored.txt\n"), 0o644), "write .dockerignore")

	build := DockerBuildSpec{
		ContextDir:     contextDir,
		DockerfilePath: dockerfilePath,
		Image:          DockerImageReference{Tag: "ghcr.io/test/app:1.0.0", Registry: "ghcr.io/test", ImageName: "app"},
	}

	before, err := computeBuildFingerprint(build)
	requireNoError(t, err, "fingerprint before")

	requireNoError(t, os.WriteFile(filepath.Join(srcDir, "ignored.txt"), []byte("more noise\n"), 0o644), "modify ignored.txt")
	after, err := computeBuildFingerprint(build)
	requireNoError(t, err, "fingerprint after ignore change")

	if before != after {
		t.Fatalf("fingerprint must be unaffected by .dockerignore-matching files: %q vs %q", before, after)
	}
}

func TestComputeBuildFingerprintHonorsGitignore(t *testing.T) {
	contextDir := t.TempDir()
	dockerfilePath := filepath.Join(contextDir, "Dockerfile")
	requireNoError(t, os.WriteFile(dockerfilePath, []byte("FROM scratch\nCOPY src /src\n"), 0o644), "write Dockerfile")
	srcDir := filepath.Join(contextDir, "src")
	distDir := filepath.Join(srcDir, "build", "dist")
	requireNoError(t, os.MkdirAll(distDir, 0o755), "mkdir dist")
	requireNoError(t, os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package main\n"), 0o644), "write main.go")
	requireNoError(t, os.WriteFile(filepath.Join(distDir, "out.bin"), []byte("artifact\n"), 0o644), "write artifact")
	requireNoError(t, os.WriteFile(filepath.Join(contextDir, ".gitignore"), []byte("/src/build/**/dist/\n*.log\n"), 0o644), "write .gitignore")

	build := DockerBuildSpec{
		ContextDir:     contextDir,
		DockerfilePath: dockerfilePath,
		Image:          DockerImageReference{Tag: "ghcr.io/test/app:1.0.0", Registry: "ghcr.io/test", ImageName: "app"},
	}

	before, err := computeBuildFingerprint(build)
	requireNoError(t, err, "fingerprint before")

	requireNoError(t, os.WriteFile(filepath.Join(distDir, "out.bin"), []byte("changed artifact\n"), 0o644), "modify artifact")
	requireNoError(t, os.WriteFile(filepath.Join(srcDir, "debug.log"), []byte("noise\n"), 0o644), "add log file")
	after, err := computeBuildFingerprint(build)
	requireNoError(t, err, "fingerprint after gitignore-matching change")

	if before != after {
		t.Fatalf("fingerprint must be unaffected by .gitignore-matching files: %q vs %q", before, after)
	}
}

func TestApplyIncrementalPromotionMarksUnchangedBuildAsPromote(t *testing.T) {
	contextDir := t.TempDir()
	dockerfilePath := filepath.Join(contextDir, "Dockerfile")
	requireNoError(t, os.WriteFile(dockerfilePath, []byte("FROM scratch\n"), 0o644), "write Dockerfile")

	build := DockerBuildSpec{
		ContextDir:     contextDir,
		DockerfilePath: dockerfilePath,
		Image:          DockerImageReference{Tag: "ghcr.io/test/app:1.0.0", Registry: "ghcr.io/test", ImageName: "app"},
	}

	updated, err := applyIncrementalPromotion([]DockerBuildSpec{build}, func(tag string) (bool, error) {
		return true, nil
	})
	requireNoError(t, err, "applyIncrementalPromotion")
	if !updated[0].Promote {
		t.Fatalf("expected Promote=true when fingerprint tag exists, got %+v", updated[0])
	}
	if updated[0].Fingerprint == "" {
		t.Fatalf("expected non-empty fingerprint, got %+v", updated[0])
	}
}

func TestApplyIncrementalPromotionLeavesBuildAloneWhenFingerprintTagMissing(t *testing.T) {
	contextDir := t.TempDir()
	dockerfilePath := filepath.Join(contextDir, "Dockerfile")
	requireNoError(t, os.WriteFile(dockerfilePath, []byte("FROM scratch\n"), 0o644), "write Dockerfile")

	build := DockerBuildSpec{
		ContextDir:     contextDir,
		DockerfilePath: dockerfilePath,
		Image:          DockerImageReference{Tag: "ghcr.io/test/app:1.0.0", Registry: "ghcr.io/test", ImageName: "app"},
	}

	updated, err := applyIncrementalPromotion([]DockerBuildSpec{build}, func(tag string) (bool, error) {
		return false, nil
	})
	requireNoError(t, err, "applyIncrementalPromotion")
	if updated[0].Promote {
		t.Fatalf("expected Promote=false when fingerprint tag missing, got %+v", updated[0])
	}
}

func TestApplyIncrementalPromotionCascadesRebuildThroughLocalFromDependency(t *testing.T) {
	workdir := t.TempDir()
	baseDir := filepath.Join(workdir, "base")
	appDir := filepath.Join(workdir, "app")
	requireNoError(t, os.MkdirAll(baseDir, 0o755), "mkdir base")
	requireNoError(t, os.MkdirAll(appDir, 0o755), "mkdir app")
	requireNoError(t, os.WriteFile(filepath.Join(baseDir, "Dockerfile"), []byte("FROM scratch\nCOPY rev /rev\n"), 0o644), "write base Dockerfile")
	requireNoError(t, os.WriteFile(filepath.Join(baseDir, "rev"), []byte("base-rev-1\n"), 0o644), "write base rev")
	requireNoError(t, os.WriteFile(filepath.Join(appDir, "Dockerfile"), []byte("FROM ghcr.io/test/base:1.0.0\n"), 0o644), "write app Dockerfile")

	builds := []DockerBuildSpec{
		{
			ContextDir:     baseDir,
			DockerfilePath: filepath.Join(baseDir, "Dockerfile"),
			Image:          DockerImageReference{Tag: "ghcr.io/test/base:1.0.0", Registry: "ghcr.io/test", ImageName: "base"},
		},
		{
			ContextDir:     appDir,
			DockerfilePath: filepath.Join(appDir, "Dockerfile"),
			Image:          DockerImageReference{Tag: "ghcr.io/test/app:1.0.0", Registry: "ghcr.io/test", ImageName: "app"},
		},
	}

	updated, err := applyIncrementalPromotion(builds, func(tag string) (bool, error) {
		// Only the app's fp tag exists locally; base's does not. Base must
		// rebuild, and the cascade forces app to rebuild too.
		if filepath.Base(tag) == "" {
			return false, nil
		}
		// Tag for base/0 is missing, app/1 is present.
		for _, build := range builds {
			fp, _ := computeBuildFingerprint(build)
			if tag == fingerprintTag(build.Image, fp, "") && build.Image.ImageName == "app" {
				return true, nil
			}
		}
		return false, nil
	})
	requireNoError(t, err, "applyIncrementalPromotion")

	for _, build := range updated {
		if build.Promote {
			t.Fatalf("expected no promotion: base must rebuild and app must cascade, got Promote on %s", build.Image.Tag)
		}
	}
}

func TestApplyIncrementalPromotionIgnoresSkipIfExistsBaseAndPromotesDependent(t *testing.T) {
	workdir := t.TempDir()
	baseDir := filepath.Join(workdir, "base")
	appDir := filepath.Join(workdir, "app")
	requireNoError(t, os.MkdirAll(baseDir, 0o755), "mkdir base")
	requireNoError(t, os.MkdirAll(appDir, 0o755), "mkdir app")
	requireNoError(t, os.WriteFile(filepath.Join(baseDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644), "write base Dockerfile")
	requireNoError(t, os.WriteFile(filepath.Join(appDir, "Dockerfile"), []byte("FROM ghcr.io/test/base:1.0.0\n"), 0o644), "write app Dockerfile")

	builds := []DockerBuildSpec{
		{
			ContextDir:     baseDir,
			DockerfilePath: filepath.Join(baseDir, "Dockerfile"),
			Image:          DockerImageReference{Tag: "ghcr.io/test/base:1.0.0", Registry: "ghcr.io/test", ImageName: "base"},
			SkipIfExists:   true,
		},
		{
			ContextDir:     appDir,
			DockerfilePath: filepath.Join(appDir, "Dockerfile"),
			Image:          DockerImageReference{Tag: "ghcr.io/test/app:1.0.0", Registry: "ghcr.io/test", ImageName: "app"},
		},
	}

	appFingerprint, err := computeBuildFingerprint(builds[1])
	requireNoError(t, err, "compute app fingerprint")

	updated, err := applyIncrementalPromotion(builds, func(tag string) (bool, error) {
		// SkipIfExists base never gets an fp tag; only the app's fp tag exists.
		return tag == fingerprintTag(builds[1].Image, appFingerprint, ""), nil
	})
	requireNoError(t, err, "applyIncrementalPromotion")

	if updated[0].Promote {
		t.Fatalf("SkipIfExists base must not be promoted: %+v", updated[0])
	}
	if updated[0].Fingerprint != "" {
		t.Fatalf("SkipIfExists base must not get a fingerprint: %+v", updated[0])
	}
	if !updated[1].Promote {
		t.Fatalf("dependent app must promote when its fp tag exists; SkipIfExists base must not cascade-invalidate it: %+v", updated[1])
	}
}
