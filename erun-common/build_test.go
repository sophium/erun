package eruncommon

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestDockerBuildArgsIncludeImageVersionAsBuildArg(t *testing.T) {
	args := dockerBuildArgs(DockerBuildSpec{
		DockerfilePath: "/tmp/Dockerfile",
		Image: DockerImageReference{
			Tag: "erunpaas/erun-devops:1.0.0-snapshot-20260406123456",
		},
	})
	got := strings.Join(args, " ")
	for _, want := range []string{
		"build",
		"-t erunpaas/erun-devops:1.0.0-snapshot-20260406123456",
		"--build-arg ERUN_VERSION=1.0.0-snapshot-20260406123456",
		"-f /tmp/Dockerfile .",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected docker build args to contain %q, got %q", want, got)
		}
	}
}

func TestDockerBuildArgsUseBuildxForMultiPlatformPush(t *testing.T) {
	args := dockerBuildArgs(DockerBuildSpec{
		DockerfilePath: "/tmp/Dockerfile",
		Image: DockerImageReference{
			Tag: "erunpaas/erun-devops:1.0.0",
		},
		Platforms: []string{"linux/amd64", "linux/arm64"},
		Push:      true,
	})
	got := strings.Join(args, " ")
	for _, want := range []string{
		"buildx build",
		"--builder erun-multiarch",
		"--platform linux/amd64,linux/arm64",
		"-t erunpaas/erun-devops:1.0.0",
		"--build-arg ERUN_VERSION=1.0.0",
		"--push",
		"-f /tmp/Dockerfile .",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected docker buildx args to contain %q, got %q", want, got)
		}
	}
}

func TestDockerBuildTraceCommandsIncludeBuildxBootstrapForMultiPlatformBuilds(t *testing.T) {
	buildInput := DockerBuildSpec{
		ContextDir:     "/tmp/project",
		DockerfilePath: "/tmp/project/Dockerfile",
		Image: DockerImageReference{
			Tag: "erunpaas/erun-devops:1.0.0",
		},
		Platforms: []string{"linux/amd64", "linux/arm64"},
		Push:      true,
	}

	commands := buildInput.traceCommands()
	if len(commands) != 4 {
		t.Fatalf("unexpected trace commands: %+v", commands)
	}
	if got := strings.Join(commands[0].Args, " "); got != "buildx inspect erun-multiarch" {
		t.Fatalf("unexpected inspect command: %q", got)
	}
	if got := strings.Join(commands[1].Args, " "); got != "buildx create --name erun-multiarch --driver docker-container" {
		t.Fatalf("unexpected create command: %q", got)
	}
	if got := strings.Join(commands[2].Args, " "); got != "buildx inspect --builder erun-multiarch --bootstrap" {
		t.Fatalf("unexpected bootstrap command: %q", got)
	}
	if got := strings.Join(commands[3].Args, " "); !strings.Contains(got, "buildx build --builder erun-multiarch --platform linux/amd64,linux/arm64") {
		t.Fatalf("unexpected build command: %q", got)
	}
}

func TestMissingBuildxPlatformsReportsRequiredPlatformsNotPresent(t *testing.T) {
	output := `Name: erun-multiarch
Driver: docker-container
Platforms: linux/arm64
`

	missing := missingBuildxPlatforms(output, []string{"linux/amd64", "linux/arm64"})
	if !reflect.DeepEqual(missing, []string{"linux/amd64"}) {
		t.Fatalf("unexpected missing platforms: %+v", missing)
	}
}

func TestOrderedDockerBuildSpecsBuildsLocalBaseImagesBeforeDependents(t *testing.T) {
	workdir := t.TempDir()
	baseDir := filepath.Join(workdir, "base")
	appDir := filepath.Join(workdir, "app")
	for _, dir := range []string{baseDir, appDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(baseDir, "Dockerfile"), []byte("FROM alpine:3.22\n"), 0o644); err != nil {
		t.Fatalf("write base Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "Dockerfile"), []byte("FROM erunpaas/base:1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write app Dockerfile: %v", err)
	}

	builds := []DockerBuildSpec{
		{
			ContextDir:     appDir,
			DockerfilePath: filepath.Join(appDir, "Dockerfile"),
			Image: DockerImageReference{
				Tag: "erunpaas/app:1.0.0",
			},
		},
		{
			ContextDir:     baseDir,
			DockerfilePath: filepath.Join(baseDir, "Dockerfile"),
			Image: DockerImageReference{
				Tag: "erunpaas/base:1.0.0",
			},
		},
	}

	ordered := orderedDockerBuildSpecs(builds)
	got := []string{ordered[0].Image.Tag, ordered[1].Image.Tag}
	want := []string{"erunpaas/base:1.0.0", "erunpaas/app:1.0.0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected build order: got %+v want %+v", got, want)
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
	if err := RunBuildExecution(ctx, execution, func(dir, path string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
		called = true
		if dir != projectRoot || path != "./build.sh" {
			t.Fatalf("unexpected script call: dir=%q path=%q", dir, path)
		}
		if len(env) != 0 {
			t.Fatalf("unexpected script env: %+v", env)
		}
		return nil
	}, func(DockerBuildSpec, io.Writer, io.Writer) error {
		t.Fatal("unexpected docker build")
		return nil
	}, nil); err != nil {
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
	if err := RunBuildExecution(ctx, execution, func(dir, path string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
		called = true
		if dir != projectRoot || path != "./build.sh" {
			t.Fatalf("unexpected script call: dir=%q path=%q", dir, path)
		}
		if len(env) != 0 {
			t.Fatalf("unexpected script env: %+v", env)
		}
		return nil
	}, func(DockerBuildSpec, io.Writer, io.Writer) error {
		t.Fatal("unexpected docker build")
		return nil
	}, nil); err != nil {
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
	if err := RunBuildExecution(ctx, execution, func(dir, path string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
		called = true
		if dir != firstDir || path != "./build.sh" {
			t.Fatalf("unexpected script call: dir=%q path=%q", dir, path)
		}
		if len(env) != 0 {
			t.Fatalf("unexpected script env: %+v", env)
		}
		return nil
	}, func(DockerBuildSpec, io.Writer, io.Writer) error {
		t.Fatal("unexpected docker build")
		return nil
	}, nil); err != nil {
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

func TestResolveBuildExecutionIncludesLinuxBuildScriptsAtProjectRoot(t *testing.T) {
	prevGOOS := currentGOOS
	prevLookPath := hostLookPath
	currentGOOS = func() string { return "linux" }
	hostLookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	t.Cleanup(func() {
		currentGOOS = prevGOOS
		hostLookPath = prevLookPath
	})

	projectRoot := t.TempDir()
	linuxComponentDir := filepath.Join(projectRoot, "erun-devops", "linux", "erun-cli")
	if err := os.MkdirAll(linuxComponentDir, 0o755); err != nil {
		t.Fatalf("mkdir linux component dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.2.3\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(linuxComponentDir, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write build.sh: %v", err)
	}

	execution, err := ResolveBuildExecution(
		ConfigStore{},
		func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		func() (DockerBuildContext, error) {
			return DockerBuildContext{Dir: projectRoot}, nil
		},
		nil,
		DockerCommandTarget{},
	)
	if err != nil {
		t.Fatalf("ResolveBuildExecution failed: %v", err)
	}

	if len(execution.linuxBuilds) != 1 {
		t.Fatalf("unexpected linux build scripts: %+v", execution.linuxBuilds)
	}
	if execution.linuxBuilds[0].Dir != linuxComponentDir || execution.linuxBuilds[0].Path != "./build.sh" {
		t.Fatalf("unexpected linux build script: %+v", execution.linuxBuilds[0])
	}
}

func TestResolveBuildExecutionSkipsLinuxBuildScriptsWhenHostIsNotLinux(t *testing.T) {
	prevGOOS := currentGOOS
	prevLookPath := hostLookPath
	currentGOOS = func() string { return "darwin" }
	hostLookPath = func(file string) (string, error) { return "/usr/bin/" + file, nil }
	t.Cleanup(func() {
		currentGOOS = prevGOOS
		hostLookPath = prevLookPath
	})

	projectRoot := t.TempDir()
	linuxComponentDir := filepath.Join(projectRoot, "erun-devops", "linux", "erun-cli")
	if err := os.MkdirAll(linuxComponentDir, 0o755); err != nil {
		t.Fatalf("mkdir linux component dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.2.3\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(linuxComponentDir, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write build.sh: %v", err)
	}

	execution, err := ResolveBuildExecution(
		ConfigStore{},
		func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		func() (DockerBuildContext, error) {
			return DockerBuildContext{Dir: projectRoot}, nil
		},
		nil,
		DockerCommandTarget{},
	)
	if err != nil {
		t.Fatalf("ResolveBuildExecution failed: %v", err)
	}
	if len(execution.linuxBuilds) != 0 || !execution.skippedLinux {
		t.Fatalf("expected linux build scripts to be skipped, got %+v", execution)
	}
}

func TestResolveBuildExecutionSkipsLinuxBuildScriptsWhenDpkgDebUnavailable(t *testing.T) {
	prevGOOS := currentGOOS
	prevLookPath := hostLookPath
	currentGOOS = func() string { return "linux" }
	hostLookPath = func(file string) (string, error) {
		return "", errors.New("not found")
	}
	t.Cleanup(func() {
		currentGOOS = prevGOOS
		hostLookPath = prevLookPath
	})

	projectRoot := t.TempDir()
	linuxComponentDir := filepath.Join(projectRoot, "erun-devops", "linux", "erun-cli")
	if err := os.MkdirAll(linuxComponentDir, 0o755); err != nil {
		t.Fatalf("mkdir linux component dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.2.3\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(linuxComponentDir, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write build.sh: %v", err)
	}

	execution, err := ResolveBuildExecution(
		ConfigStore{},
		func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		func() (DockerBuildContext, error) {
			return DockerBuildContext{Dir: projectRoot}, nil
		},
		nil,
		DockerCommandTarget{},
	)
	if err != nil {
		t.Fatalf("ResolveBuildExecution failed: %v", err)
	}
	if len(execution.linuxBuilds) != 0 || !execution.skippedLinux {
		t.Fatalf("expected linux build scripts to be skipped, got %+v", execution)
	}
}

func TestRunBuildExecutionRunsLinuxBuildScripts(t *testing.T) {
	linuxComponentDir := t.TempDir()
	execution := BuildExecutionSpec{
		linuxBuilds: []scriptSpec{{
			Dir:  linuxComponentDir,
			Path: "./build.sh",
			Env:  []string{"ERUN_BUILD_VERSION=1.2.3"},
		}},
	}

	var called bool
	ctx := Context{
		Logger: NewLoggerWithWriters(2, io.Discard, io.Discard),
		Stdin:  new(bytes.Buffer),
		Stdout: new(bytes.Buffer),
		Stderr: new(bytes.Buffer),
	}
	if err := RunBuildExecution(ctx, execution, func(dir, path string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
		called = true
		if dir != linuxComponentDir || path != "./build.sh" {
			t.Fatalf("unexpected script call: dir=%q path=%q", dir, path)
		}
		if len(env) != 1 || env[0] != "ERUN_BUILD_VERSION=1.2.3" {
			t.Fatalf("unexpected script env: %+v", env)
		}
		return nil
	}, func(DockerBuildSpec, io.Writer, io.Writer) error {
		t.Fatal("unexpected docker build")
		return nil
	}, nil); err != nil {
		t.Fatalf("RunBuildExecution failed: %v", err)
	}
	if !called {
		t.Fatal("expected linux build script runner to be called")
	}
}

func TestResolveCurrentDockerBuildContextsUsesProjectRootDevopsModule(t *testing.T) {
	projectRoot := t.TempDir()
	moduleRoot := filepath.Join(projectRoot, "tenant-a-devops")
	componentDir := filepath.Join(moduleRoot, "docker", "tenant-a-devops")
	if err := os.MkdirAll(componentDir, 0o755); err != nil {
		t.Fatalf("mkdir component dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}

	buildContexts, err := ResolveCurrentDockerBuildContexts(
		func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		func() (DockerBuildContext, error) {
			return DockerBuildContext{Dir: projectRoot}, nil
		},
		DockerCommandTarget{},
	)
	if err != nil {
		t.Fatalf("ResolveCurrentDockerBuildContexts failed: %v", err)
	}
	if len(buildContexts) != 1 || buildContexts[0].Dir != componentDir {
		t.Fatalf("unexpected build contexts: %+v", buildContexts)
	}
}

func TestResolveCurrentDockerBuildContextsUsesDevopsModuleRoot(t *testing.T) {
	projectRoot := t.TempDir()
	moduleRoot := filepath.Join(projectRoot, "tenant-a-devops")
	componentDir := filepath.Join(moduleRoot, "docker", "tenant-a-devops")
	if err := os.MkdirAll(componentDir, 0o755); err != nil {
		t.Fatalf("mkdir component dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}

	buildContexts, err := ResolveCurrentDockerBuildContexts(
		func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		func() (DockerBuildContext, error) {
			return DockerBuildContext{Dir: moduleRoot}, nil
		},
		DockerCommandTarget{},
	)
	if err != nil {
		t.Fatalf("ResolveCurrentDockerBuildContexts failed: %v", err)
	}
	if len(buildContexts) != 1 || buildContexts[0].Dir != componentDir {
		t.Fatalf("unexpected build contexts: %+v", buildContexts)
	}
}

func TestResolveBuildExecutionBuildsWithoutPushesForProjectRootDevopsScope(t *testing.T) {
	projectRoot := t.TempDir()
	moduleRoot := filepath.Join(projectRoot, "tenant-a-devops")
	componentDirs := []string{
		filepath.Join(moduleRoot, "docker", "tenant-a-devops"),
		filepath.Join(moduleRoot, "docker", "erun-dind"),
	}
	for _, dir := range componentDirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir component dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
			t.Fatalf("write Dockerfile: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(componentDirs[0], "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDirs[1], "VERSION"), []byte("28.1.1\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := SaveProjectConfig(projectRoot, ProjectConfig{
		Environments: map[string]ProjectEnvironmentConfig{
			DefaultEnvironment: {ContainerRegistry: "erunpaas"},
		},
	}); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	execution, err := ResolveBuildExecution(
		ConfigStore{},
		func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		func() (DockerBuildContext, error) {
			return DockerBuildContext{Dir: projectRoot}, nil
		},
		nil,
		DockerCommandTarget{Environment: DefaultEnvironment},
	)
	if err != nil {
		t.Fatalf("ResolveBuildExecution failed: %v", err)
	}

	if len(execution.dockerBuilds) != 2 || len(execution.dockerPushes) != 0 {
		t.Fatalf("unexpected execution: %+v", execution)
	}
	buildTags := []string{execution.dockerBuilds[0].Image.Tag, execution.dockerBuilds[1].Image.Tag}
	wantTags := []string{"erunpaas/tenant-a-devops:1.0.0", "erunpaas/erun-dind:28.1.1"}
	for _, want := range wantTags {
		found := false
		for _, got := range buildTags {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing build tag %q in %+v", want, execution.dockerBuilds)
		}
	}
}

func TestResolveDockerPushSpecRejectsNonDockerfileScopes(t *testing.T) {
	projectRoot := t.TempDir()
	moduleRoot := filepath.Join(projectRoot, "tenant-a-devops")
	dockerDir := filepath.Join(moduleRoot, "docker")
	componentDir := filepath.Join(dockerDir, "tenant-a-devops")
	if err := os.MkdirAll(componentDir, 0o755); err != nil {
		t.Fatalf("mkdir component dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}

	for _, scope := range []string{projectRoot, moduleRoot, dockerDir} {
		_, _, err := ResolveDockerPushSpec(
			ConfigStore{},
			func() (string, string, error) {
				return "tenant-a", projectRoot, nil
			},
			func() (DockerBuildContext, error) {
				return DockerBuildContext{Dir: scope}, nil
			},
			nil,
			DockerCommandTarget{Environment: DefaultEnvironment},
		)
		if err == nil {
			t.Fatalf("expected error for scope %q", scope)
		}
		if err.Error() != "dockerfile not found in current directory" {
			t.Fatalf("unexpected error for scope %q: %v", scope, err)
		}
	}
}

func TestResolveBuildExecutionReleaseUsesResolvedVersionForDockerBuilds(t *testing.T) {
	projectRoot := setupReleaseProjectGitRepo(t, "main")
	buildDir := filepath.Join(projectRoot, "erun-devops", "docker", "api")

	execution, err := ResolveBuildExecution(
		ConfigStore{},
		func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		func() (DockerBuildContext, error) {
			return DockerBuildContextAtDir(buildDir)
		},
		nil,
		DockerCommandTarget{ProjectRoot: projectRoot, Environment: DefaultEnvironment, Release: true},
	)
	if err != nil {
		t.Fatalf("ResolveBuildExecution failed: %v", err)
	}

	if execution.release == nil {
		t.Fatalf("expected release spec, got %+v", execution)
	}
	if got := execution.release.Version; got != "1.4.2" {
		t.Fatalf("unexpected release version: %q", got)
	}
	if len(execution.dockerBuilds) != 1 {
		t.Fatalf("unexpected docker builds: %+v", execution.dockerBuilds)
	}
	if got := execution.dockerBuilds[0].Image.Tag; got != "erunpaas/api:1.4.2" {
		t.Fatalf("unexpected docker build tag: %q", got)
	}
	if !execution.dockerBuilds[0].Push || !reflect.DeepEqual(execution.dockerBuilds[0].Platforms, []string{"linux/amd64", "linux/arm64"}) {
		t.Fatalf("expected multi-platform release build spec, got %+v", execution.dockerBuilds[0])
	}
	if len(execution.dockerPushes) != 1 {
		t.Fatalf("unexpected docker pushes: %+v", execution.dockerPushes)
	}
	if got := execution.dockerPushes[0].Image.Tag; got != "erunpaas/api:1.4.2" {
		t.Fatalf("unexpected docker push tag: %q", got)
	}
	if got := execution.release.NextVersion; got != "1.4.3" {
		t.Fatalf("unexpected next version: %q", got)
	}
}

func TestResolveBuildExecutionReleaseOnlyPushesReleaseTaggedDockerBuilds(t *testing.T) {
	projectRoot := setupReleaseProjectGitRepo(t, "main")

	execution, err := ResolveBuildExecution(
		ConfigStore{},
		func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		func() (DockerBuildContext, error) {
			return DockerBuildContext{Dir: projectRoot}, nil
		},
		nil,
		DockerCommandTarget{ProjectRoot: projectRoot, Environment: DefaultEnvironment, Release: true},
	)
	if err != nil {
		t.Fatalf("ResolveBuildExecution failed: %v", err)
	}

	if len(execution.dockerBuilds) != 2 {
		t.Fatalf("unexpected docker builds: %+v", execution.dockerBuilds)
	}
	if len(execution.dockerPushes) != 1 {
		t.Fatalf("unexpected docker pushes: %+v", execution.dockerPushes)
	}
	if got := execution.dockerPushes[0].Image.Tag; got != "erunpaas/api:1.4.2" {
		t.Fatalf("unexpected docker push tag: %q", got)
	}
}

func TestResolveBuildExecutionReleasePushesLocalDockerDependenciesAndDind(t *testing.T) {
	projectRoot := setupReleaseProjectGitRepo(t, "main")
	releaseRoot := filepath.Join(projectRoot, "erun-devops")

	apiDockerfilePath := filepath.Join(releaseRoot, "docker", "api", "Dockerfile")
	if err := os.WriteFile(apiDockerfilePath, []byte("FROM erunpaas/base:9.9.9\n"), 0o644); err != nil {
		t.Fatalf("write api Dockerfile: %v", err)
	}

	dindDir := filepath.Join(releaseRoot, "docker", "erun-dind")
	if err := os.MkdirAll(dindDir, 0o755); err != nil {
		t.Fatalf("mkdir dind dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dindDir, "Dockerfile"), []byte("FROM docker:28.1.1-dind\n"), 0o644); err != nil {
		t.Fatalf("write dind Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dindDir, "VERSION"), []byte("28.1.1\n"), 0o644); err != nil {
		t.Fatalf("write dind VERSION: %v", err)
	}

	execution, err := ResolveBuildExecution(
		ConfigStore{},
		func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		func() (DockerBuildContext, error) {
			return DockerBuildContext{Dir: projectRoot}, nil
		},
		nil,
		DockerCommandTarget{ProjectRoot: projectRoot, Environment: DefaultEnvironment, Release: true},
	)
	if err != nil {
		t.Fatalf("ResolveBuildExecution failed: %v", err)
	}

	if len(execution.dockerPushes) != 3 {
		t.Fatalf("unexpected docker pushes: %+v", execution.dockerPushes)
	}
	wantPushes := map[string]struct{}{
		"erunpaas/api:1.4.2":        {},
		"erunpaas/base:9.9.9":       {},
		"erunpaas/erun-dind:28.1.1": {},
	}
	for _, pushInput := range execution.dockerPushes {
		if _, ok := wantPushes[pushInput.Image.Tag]; !ok {
			t.Fatalf("unexpected docker push tag: %q", pushInput.Image.Tag)
		}
		delete(wantPushes, pushInput.Image.Tag)
	}
	if len(wantPushes) != 0 {
		t.Fatalf("missing docker pushes: %+v", wantPushes)
	}

	for _, build := range execution.dockerBuilds {
		switch build.Image.Tag {
		case "erunpaas/api:1.4.2", "erunpaas/base:9.9.9", "erunpaas/erun-dind:28.1.1":
			if !build.Push || !reflect.DeepEqual(build.Platforms, []string{"linux/amd64", "linux/arm64"}) {
				t.Fatalf("expected multi-platform release build for %q, got %+v", build.Image.Tag, build)
			}
		}
	}
}

func TestResolveBuildExecutionReleasePassesVersionToProjectBuildScript(t *testing.T) {
	projectRoot := setupReleaseProjectGitRepo(t, "develop")
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
		DockerCommandTarget{ProjectRoot: projectRoot, Environment: DefaultEnvironment, Release: true},
	)
	if err != nil {
		t.Fatalf("ResolveBuildExecution failed: %v", err)
	}

	if execution.release == nil || execution.script == nil {
		t.Fatalf("unexpected execution: %+v", execution)
	}
	if got := execution.script.Env; len(got) != 1 || !strings.HasPrefix(got[0], "ERUN_BUILD_VERSION=1.4.2-rc.") {
		t.Fatalf("unexpected script env: %+v", got)
	}
}

func TestRunBuildExecutionDryRunReleaseIncludesReleaseAndBuildTrace(t *testing.T) {
	projectRoot := setupReleaseProjectGitRepo(t, "develop")
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
		DockerCommandTarget{ProjectRoot: projectRoot, Environment: DefaultEnvironment, Release: true},
	)
	if err != nil {
		t.Fatalf("ResolveBuildExecution failed: %v", err)
	}

	stdout := new(bytes.Buffer)
	ctx := Context{
		Logger: NewLoggerWithWriters(2, stdout, io.Discard),
		DryRun: true,
		Stdin:  new(bytes.Buffer),
		Stdout: stdout,
		Stderr: io.Discard,
	}
	if err := RunBuildExecution(ctx, execution, nil, nil, nil); err != nil {
		t.Fatalf("RunBuildExecution failed: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "release: branch=develop mode=candidate version=1.4.2-rc.") {
		t.Fatalf("expected release trace, got:\n%s", output)
	}
	if !strings.Contains(output, "ERUN_BUILD_VERSION=1.4.2-rc.") || !strings.Contains(output, "./build.sh") {
		t.Fatalf("expected build script trace with version env, got:\n%s", output)
	}
	if !strings.Contains(output, "release version: 1.4.2-rc.") {
		t.Fatalf("expected final release version output, got:\n%s", output)
	}
}

func TestRunBuildExecutionReleasePublishesResolvedVersionAsMultiPlatformBuild(t *testing.T) {
	projectRoot := setupReleaseProjectGitRepo(t, "main")
	buildDir := filepath.Join(projectRoot, "erun-devops", "docker", "api")

	execution, err := ResolveBuildExecution(
		ConfigStore{},
		func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		func() (DockerBuildContext, error) {
			return DockerBuildContextAtDir(buildDir)
		},
		nil,
		DockerCommandTarget{ProjectRoot: projectRoot, Environment: DefaultEnvironment, Release: true},
	)
	if err != nil {
		t.Fatalf("ResolveBuildExecution failed: %v", err)
	}
	execution.release = nil

	var buildCalls []DockerBuildSpec
	var pushCalls []string
	ctx := Context{
		Logger: NewLoggerWithWriters(2, io.Discard, io.Discard),
		Stdin:  new(bytes.Buffer),
		Stdout: new(bytes.Buffer),
		Stderr: new(bytes.Buffer),
	}
	if err := RunBuildExecution(ctx, execution, nil, func(buildInput DockerBuildSpec, stdout, stderr io.Writer) error {
		buildCalls = append(buildCalls, buildInput)
		return nil
	}, func(ctx Context, pushInput DockerPushSpec) error {
		pushCalls = append(pushCalls, pushInput.Image.Tag)
		return nil
	}); err != nil {
		t.Fatalf("RunBuildExecution failed: %v", err)
	}

	if len(buildCalls) != 1 || buildCalls[0].Image.Tag != "erunpaas/api:1.4.2" {
		t.Fatalf("unexpected build calls: %+v", buildCalls)
	}
	if !buildCalls[0].Push || !reflect.DeepEqual(buildCalls[0].Platforms, []string{"linux/amd64", "linux/arm64"}) {
		t.Fatalf("expected multi-platform pushed release build, got %+v", buildCalls[0])
	}
	if len(pushCalls) != 0 {
		t.Fatalf("did not expect separate push calls: %+v", pushCalls)
	}
}

func TestRunBuildExecutionAndDeployDryRunReleaseReportsDeployedVersionLast(t *testing.T) {
	projectRoot := setupReleaseProjectGitRepo(t, "develop")
	chartPath := filepath.Join(projectRoot, "erun-devops", "k8s", "api")
	if err := os.WriteFile(filepath.Join(chartPath, "values.local.yaml"), nil, 0o644); err != nil {
		t.Fatalf("write values.local.yaml: %v", err)
	}
	if err := SaveTenantConfig(TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := SaveEnvConfig("tenant-a", EnvConfig{
		Name:              DefaultEnvironment,
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}
	if err := SaveProjectConfig(projectRoot, ProjectConfig{}); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	findProjectRoot := func() (string, string, error) {
		return "tenant-a", projectRoot, nil
	}
	execution, err := ResolveBuildExecution(
		ConfigStore{},
		findProjectRoot,
		func() (DockerBuildContext, error) {
			return DockerBuildContextAtDir(filepath.Join(projectRoot, "erun-devops", "docker", "api"))
		},
		nil,
		DockerCommandTarget{ProjectRoot: projectRoot, Environment: DefaultEnvironment, Release: true},
	)
	if err != nil {
		t.Fatalf("ResolveBuildExecution failed: %v", err)
	}
	deploySpec, err := ResolveDeploySpecForDockerTarget(
		ConfigStore{},
		findProjectRoot,
		func() (DockerBuildContext, error) {
			return DockerBuildContextAtDir(filepath.Join(projectRoot, "erun-devops", "docker", "api"))
		},
		func() (KubernetesDeployContext, error) {
			return KubernetesDeployContextAtDir(filepath.Join(projectRoot, "erun-devops", "docker", "api")), nil
		},
		nil,
		DockerCommandTarget{ProjectRoot: projectRoot, Environment: DefaultEnvironment, Release: true},
		"api",
	)
	if err != nil {
		t.Fatalf("ResolveDeploySpecForDockerTarget failed: %v", err)
	}
	if deploySpec.Deploy.Version != "1.4.2-rc.0000000" && !strings.HasPrefix(deploySpec.Deploy.Version, "1.4.2-rc.") {
		t.Fatalf("unexpected deploy version: %+v", deploySpec.Deploy)
	}

	stdout := new(bytes.Buffer)
	ctx := Context{
		Logger: NewLoggerWithWriters(2, stdout, io.Discard),
		DryRun: true,
		Stdin:  new(bytes.Buffer),
		Stdout: stdout,
		Stderr: io.Discard,
	}
	if err := RunBuildExecutionAndDeploy(ctx, execution, []DeploySpec{deploySpec}, nil, nil, nil, func(HelmDeployParams) error {
		t.Fatal("unexpected deploy execution during dry-run")
		return nil
	}); err != nil {
		t.Fatalf("RunBuildExecutionAndDeploy failed: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	if !strings.Contains(output, "release version: 1.4.2-rc.") {
		t.Fatalf("expected release version output, got:\n%s", output)
	}
	if !strings.Contains(output, "deployed version: 1.4.2-rc.") {
		t.Fatalf("expected deployed version output, got:\n%s", output)
	}
	lines := strings.Split(output, "\n")
	if !strings.Contains(lines[len(lines)-1], "deployed version: 1.4.2-rc.") {
		t.Fatalf("expected deployed version last, got:\n%s", output)
	}
}

func setupReleaseProjectGitRepo(t *testing.T, branch string) string {
	t.Helper()

	projectRoot := setupReleaseProject(t, releaseProjectOptions{})
	runGitWithEnv(t, projectRoot, nil, "init", "-b", branch)
	runGitWithEnv(t, projectRoot, nil, "config", "user.email", "codex@example.com")
	runGitWithEnv(t, projectRoot, nil, "config", "user.name", "Codex")
	runGitWithEnv(t, projectRoot, nil, "add", ".")
	runGitWithEnv(t, projectRoot, nil, "commit", "-m", "initial")
	return projectRoot
}
