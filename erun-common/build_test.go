package eruncommon

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveDockerBuildContextDirForProjectUsesProjectRootForModuleDockerDirs(t *testing.T) {
	projectRoot := t.TempDir()
	buildDir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}

	contextDir := ResolveDockerBuildContextDirForProject(buildDir, projectRoot)
	if contextDir != projectRoot {
		t.Fatalf("unexpected context dir: %q", contextDir)
	}
}

func TestResolveDockerBuildContextDetectsDockerfile(t *testing.T) {
	workdir := t.TempDir()
	dockerfilePath := filepath.Join(workdir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}

	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousDir) })
	if err := os.Chdir(workdir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	result, err := ResolveDockerBuildContext()
	if err != nil {
		t.Fatalf("ResolveDockerBuildContext failed: %v", err)
	}
	resolvedWorkdir, err := filepath.EvalSymlinks(workdir)
	if err != nil {
		t.Fatalf("EvalSymlinks(workdir) failed: %v", err)
	}
	resolvedDockerfilePath, err := filepath.EvalSymlinks(dockerfilePath)
	if err != nil {
		t.Fatalf("EvalSymlinks(dockerfilePath) failed: %v", err)
	}
	if result.Dir != resolvedWorkdir || result.DockerfilePath != resolvedDockerfilePath {
		t.Fatalf("unexpected build context: %+v", result)
	}
}

func TestDockerRegistryFromImageTag(t *testing.T) {
	tests := map[string]string{
		"erunpaas/erun-ubuntu:noble-20260217":    "",
		"ghcr.io/acme/erun-devops:1.0.0":         "ghcr.io",
		"localhost:5000/erun-devops:1.0.0":       "localhost:5000",
		"registry.example.com/team/image:latest": "registry.example.com",
	}

	for tag, want := range tests {
		if got := dockerRegistryFromImageTag(tag); got != want {
			t.Fatalf("dockerRegistryFromImageTag(%q) = %q, want %q", tag, got, want)
		}
	}
}

func TestResolveDockerBuildContextIgnoresMissingDockerfile(t *testing.T) {
	workdir := t.TempDir()

	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousDir) })
	if err := os.Chdir(workdir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	result, err := ResolveDockerBuildContext()
	if err != nil {
		t.Fatalf("ResolveDockerBuildContext failed: %v", err)
	}
	resolvedWorkdir, err := filepath.EvalSymlinks(workdir)
	if err != nil {
		t.Fatalf("EvalSymlinks(workdir) failed: %v", err)
	}
	if result.Dir != resolvedWorkdir {
		t.Fatalf("unexpected build context: %+v", result)
	}
	if result.DockerfilePath != "" {
		t.Fatalf("expected empty Dockerfile path, got %+v", result)
	}
}

func TestResolveBuildExecutionPrefersProjectBuildScript(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write build.sh: %v", err)
	}

	execution, err := ResolveBuildExecution(
		ConfigStore{},
		func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		func() (DockerBuildContext, error) {
			return DockerBuildContext{}, errors.New("docker build context should not be resolved")
		},
		nil,
		DockerCommandTarget{},
	)
	if err != nil {
		t.Fatalf("ResolveBuildExecution failed: %v", err)
	}

	var called bool
	ctx := Context{
		Logger: NewLoggerWithWriters(2, io.Discard, io.Discard),
		Stdin:  new(bytes.Buffer),
		Stdout: new(bytes.Buffer),
		Stderr: new(bytes.Buffer),
	}
	if err := RunBuildExecution(ctx, execution, func(dir, path string, stdin io.Reader, stdout, stderr io.Writer) error {
		called = true
		if dir != projectRoot || path != "./build.sh" {
			t.Fatalf("unexpected script call: dir=%q path=%q", dir, path)
		}
		return nil
	}, func(string, string, string, io.Writer, io.Writer) error {
		t.Fatal("unexpected docker build")
		return nil
	}); err != nil {
		t.Fatalf("RunBuildExecution failed: %v", err)
	}
	if !called {
		t.Fatal("expected build script runner to be called")
	}
}

func TestResolveBuildExecutionPrefersProjectRootBuildScriptOverNestedScripts(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write root build.sh: %v", err)
	}
	nestedDir := filepath.Join(projectRoot, "scripts", "alpha")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("mkdir nested dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write nested build.sh: %v", err)
	}

	execution, err := ResolveBuildExecution(
		ConfigStore{},
		func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		func() (DockerBuildContext, error) {
			return DockerBuildContext{}, errors.New("docker build context should not be resolved")
		},
		nil,
		DockerCommandTarget{},
	)
	if err != nil {
		t.Fatalf("ResolveBuildExecution failed: %v", err)
	}

	var called bool
	ctx := Context{
		Logger: NewLoggerWithWriters(2, io.Discard, io.Discard),
		Stdin:  new(bytes.Buffer),
		Stdout: new(bytes.Buffer),
		Stderr: new(bytes.Buffer),
	}
	if err := RunBuildExecution(ctx, execution, func(dir, path string, stdin io.Reader, stdout, stderr io.Writer) error {
		called = true
		if dir != projectRoot || path != "./build.sh" {
			t.Fatalf("unexpected script call: dir=%q path=%q", dir, path)
		}
		return nil
	}, func(string, string, string, io.Writer, io.Writer) error {
		t.Fatal("unexpected docker build")
		return nil
	}); err != nil {
		t.Fatalf("RunBuildExecution failed: %v", err)
	}
	if !called {
		t.Fatal("expected build script runner to be called")
	}
}

func TestResolveBuildExecutionUsesFirstNestedProjectBuildScript(t *testing.T) {
	projectRoot := t.TempDir()
	firstDir := filepath.Join(projectRoot, "scripts", "alpha")
	if err := os.MkdirAll(firstDir, 0o755); err != nil {
		t.Fatalf("mkdir first dir: %v", err)
	}
	secondDir := filepath.Join(projectRoot, "scripts", "zeta")
	if err := os.MkdirAll(secondDir, 0o755); err != nil {
		t.Fatalf("mkdir second dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(firstDir, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write first build.sh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(secondDir, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write second build.sh: %v", err)
	}

	execution, err := ResolveBuildExecution(
		ConfigStore{},
		func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		func() (DockerBuildContext, error) {
			return DockerBuildContext{}, errors.New("docker build context should not be resolved")
		},
		nil,
		DockerCommandTarget{},
	)
	if err != nil {
		t.Fatalf("ResolveBuildExecution failed: %v", err)
	}

	var called bool
	ctx := Context{
		Logger: NewLoggerWithWriters(2, io.Discard, io.Discard),
		Stdin:  new(bytes.Buffer),
		Stdout: new(bytes.Buffer),
		Stderr: new(bytes.Buffer),
	}
	if err := RunBuildExecution(ctx, execution, func(dir, path string, stdin io.Reader, stdout, stderr io.Writer) error {
		called = true
		if dir != firstDir || path != "./build.sh" {
			t.Fatalf("unexpected script call: dir=%q path=%q", dir, path)
		}
		return nil
	}, func(string, string, string, io.Writer, io.Writer) error {
		t.Fatal("unexpected docker build")
		return nil
	}); err != nil {
		t.Fatalf("RunBuildExecution failed: %v", err)
	}
	if !called {
		t.Fatal("expected build script runner to be called")
	}
}

func TestHasProjectBuildScriptIgnoresDockerArtifactBuildScripts(t *testing.T) {
	projectRoot := t.TempDir()
	artifactDir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatalf("mkdir artifact dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write artifact build.sh: %v", err)
	}

	hasScript, err := HasProjectBuildScript(func() (string, string, error) {
		return "tenant-a", projectRoot, nil
	}, DockerCommandTarget{})
	if err != nil {
		t.Fatalf("HasProjectBuildScript failed: %v", err)
	}
	if hasScript {
		t.Fatal("did not expect docker artifact build.sh to be selected")
	}
}
