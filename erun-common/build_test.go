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
	"time"
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

func TestResolveBuildExecutionMarksEnvironmentConfiguredBuildsSkippable(t *testing.T) {
	projectRoot := t.TempDir()
	buildDir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	writeDockerBuildFixture(t, buildDir)
	writeVersionFileForTest(t, filepath.Join(projectRoot, "erun-devops", "VERSION"), "1.0.0")
	requireNoError(t, SaveProjectConfig(projectRoot, ProjectConfig{
		Environments: map[string]ProjectEnvironmentConfig{
			DefaultEnvironment: {
				ContainerRegistry: "erunpaas",
				Docker: ProjectDockerConfig{
					SkipIfExists: []string{"erunpaas/erun-devops"},
				},
			},
			"frs": {
				ContainerRegistry: "erunpaas",
				Docker: ProjectDockerConfig{
					SkipIfExists: []string{"erunpaas/other"},
				},
			},
		},
	}), "SaveProjectConfig failed")

	localExecution, err := ResolveBuildExecution(
		ConfigStore{},
		func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		func() (DockerBuildContext, error) {
			return DockerBuildContextAtDir(buildDir)
		},
		func() time.Time { return time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC) },
		DockerCommandTarget{ProjectRoot: projectRoot, Environment: DefaultEnvironment},
	)
	requireNoError(t, err, "ResolveBuildExecution local failed")
	requireSkippableBuild(t, localExecution)

	frsExecution, err := ResolveBuildExecution(
		ConfigStore{},
		func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		func() (DockerBuildContext, error) {
			return DockerBuildContextAtDir(buildDir)
		},
		func() time.Time { return time.Date(2026, 4, 24, 12, 0, 0, 0, time.UTC) },
		DockerCommandTarget{ProjectRoot: projectRoot, Environment: "frs"},
	)
	requireNoError(t, err, "ResolveBuildExecution frs failed")
	requireUnskippableBuild(t, frsExecution)
}

func writeDockerBuildFixture(t *testing.T, buildDir string) {
	t.Helper()
	requireNoError(t, os.MkdirAll(buildDir, 0o755), "mkdir build dir")
	requireNoError(t, os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644), "write Dockerfile")
}

func writeVersionFileForTest(t *testing.T, path, version string) {
	t.Helper()
	requireNoError(t, os.WriteFile(path, []byte(version+"\n"), 0o644), "write VERSION")
}

func requireSkippableBuild(t *testing.T, execution BuildExecutionSpec) {
	t.Helper()
	requireEqual(t, len(execution.dockerBuilds), 1, "docker build count")
	requireCondition(t, execution.dockerBuilds[0].SkipIfExists, "expected build to be skippable, got %+v", execution.dockerBuilds)
}

func requireUnskippableBuild(t *testing.T, execution BuildExecutionSpec) {
	t.Helper()
	requireEqual(t, len(execution.dockerBuilds), 1, "docker build count")
	requireCondition(t, !execution.dockerBuilds[0].SkipIfExists, "did not expect build to be skippable, got %+v", execution.dockerBuilds)
}

func TestRunDockerBuildsSkipsConfiguredExistingLocalImages(t *testing.T) {
	builds := []DockerBuildSpec{
		{
			ContextDir:     "/tmp/base",
			DockerfilePath: "/tmp/base/Dockerfile",
			Image: DockerImageReference{
				Tag: "erunpaas/base:1.2.3",
			},
			SkipIfExists: true,
			Push:         true,
		},
		{
			ContextDir:     "/tmp/api",
			DockerfilePath: "/tmp/api/Dockerfile",
			Image: DockerImageReference{
				Tag: "erunpaas/api:1.2.3",
			},
			SkipIfExists: true,
		},
		{
			ContextDir:     "/tmp/worker",
			DockerfilePath: "/tmp/worker/Dockerfile",
			Image: DockerImageReference{
				Tag: "erunpaas/worker:1.2.3",
			},
		},
	}

	inspected := make([]string, 0)
	built := make([]string, 0)
	ctx := Context{
		Logger: NewLoggerWithWriters(1, io.Discard, io.Discard),
		Stdout: io.Discard,
		Stderr: io.Discard,
	}
	err := runDockerBuilds(ctx, builds, func(buildInput DockerBuildSpec, stdout, stderr io.Writer) error {
		built = append(built, buildInput.Image.Tag)
		return nil
	}, func(tag string) (bool, error) {
		inspected = append(inspected, tag)
		return tag == "erunpaas/base:1.2.3", nil
	})
	if err != nil {
		t.Fatalf("runDockerBuilds failed: %v", err)
	}

	if !reflect.DeepEqual(inspected, []string{"erunpaas/base:1.2.3", "erunpaas/api:1.2.3"}) {
		t.Fatalf("unexpected inspected tags: %+v", inspected)
	}
	if !reflect.DeepEqual(built, []string{"erunpaas/api:1.2.3", "erunpaas/worker:1.2.3"}) {
		t.Fatalf("unexpected built tags: %+v", built)
	}
}

func TestRunDockerBuildSkipsPushBuildWhenRemoteManifestExists(t *testing.T) {
	dockerDir := t.TempDir()
	argsPath := filepath.Join(dockerDir, "docker.args")
	dockerPath := filepath.Join(dockerDir, "docker")
	if err := os.WriteFile(dockerPath, []byte(`#!/bin/sh
echo "$@" > "`+argsPath+`"
if [ "$1" = "manifest" ] && [ "$2" = "inspect" ]; then
  exit 0
fi
echo "unexpected docker invocation: $@" >&2
exit 1
`), 0o755); err != nil {
		t.Fatalf("write docker stub: %v", err)
	}
	t.Setenv("PATH", dockerDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := RunDockerBuild(Context{
		Logger: NewLoggerWithWriters(1, io.Discard, io.Discard),
		Stdout: io.Discard,
		Stderr: io.Discard,
	}, DockerBuildSpec{
		ContextDir:     "/tmp/base",
		DockerfilePath: "/tmp/base/Dockerfile",
		Image: DockerImageReference{
			Tag: "erunpaas/erun-dind:28.1.1",
		},
		SkipIfExists: true,
		Push:         true,
	}, func(buildInput DockerBuildSpec, stdout, stderr io.Writer) error {
		t.Fatalf("build should not run for existing push manifest: %+v", buildInput)
		return nil
	})
	if err != nil {
		t.Fatalf("RunDockerBuild failed: %v", err)
	}

	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read docker args: %v", err)
	}
	if strings.TrimSpace(string(args)) != "manifest inspect erunpaas/erun-dind:28.1.1" {
		t.Fatalf("unexpected docker args: %q", strings.TrimSpace(string(args)))
	}
}

func TestDockerImageBuilderReturnsRegistryAuthErrorForBuildxPushAuthFailure(t *testing.T) {
	dockerDir := t.TempDir()
	dockerPath := filepath.Join(dockerDir, "docker")
	if err := os.WriteFile(dockerPath, []byte(`#!/bin/sh
if [ "$1" = "buildx" ] && [ "$2" = "inspect" ] && [ "$3" = "erun-multiarch" ]; then
  exit 0
fi
if [ "$1" = "buildx" ] && [ "$2" = "inspect" ] && [ "$3" = "--builder" ] && [ "$4" = "erun-multiarch" ] && [ "$5" = "--bootstrap" ]; then
  cat <<'EOF'
Name: erun-multiarch
Driver: docker-container
Platforms: linux/amd64, linux/arm64
EOF
  exit 0
fi
if [ "$1" = "buildx" ] && [ "$2" = "build" ]; then
  echo "push access denied: insufficient_scope: authorization failed" >&2
  exit 1
fi
echo "unexpected docker invocation: $@" >&2
exit 1
`), 0o755); err != nil {
		t.Fatalf("write docker stub: %v", err)
	}
	t.Setenv("PATH", dockerDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := DockerImageBuilder(DockerBuildSpec{
		ContextDir:     t.TempDir(),
		DockerfilePath: "/tmp/Dockerfile",
		Image: DockerImageReference{
			Tag: "erunpaas/erun-devops:1.4.2",
		},
		Platforms: []string{"linux/amd64", "linux/arm64"},
		Push:      true,
	}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected auth error")
	}

	var authErr DockerRegistryAuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected DockerRegistryAuthError, got %T: %v", err, err)
	}
	if authErr.Tag != "erunpaas/erun-devops:1.4.2" {
		t.Fatalf("unexpected auth error tag: %+v", authErr)
	}
	if authErr.Registry != "" {
		t.Fatalf("expected Docker Hub registry, got %q", authErr.Registry)
	}
}

func TestIsGHCRRegistryRecognizesHostnameAndNamespace(t *testing.T) {
	cases := map[string]bool{
		"":                       false,
		"docker.io":              false,
		"erunpaas":               false,
		"ghcr.io":                true,
		"GHCR.IO":                true,
		"ghcr.io/sophium":        true,
		"  ghcr.io/sophium/foo ": true,
		"123456789.dkr.ecr.us-east-1.amazonaws.com": false,
	}
	for input, want := range cases {
		if got := isGHCRRegistry(input); got != want {
			t.Errorf("isGHCRRegistry(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestTryGHCRLoginViaGHFallsBackWhenGHMissing(t *testing.T) {
	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)

	ok, err := tryGHCRLoginViaGH("ghcr.io", io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("expected nil error when gh is missing, got %v", err)
	}
	if ok {
		t.Fatalf("expected no-op when gh CLI is unavailable")
	}
}

func TestIsDockerPushAuthorizationErrorDetectsRegistryDenials(t *testing.T) {
	cases := map[string]bool{
		"unauthorized: authentication required":                                  true,
		"denied: requested access to the resource is denied":                     true,
		"insufficient_scope: authorization failed":                               true,
		"error from registry: denied\ndenied":                                    true,
		"error from registry: permission_denied: The token provided does not match expected scopes.": true,
		"errorresponse from daemon: pull access denied for image":                true,
		"failed to copy: no basic auth credentials":                              true,
		"network unreachable":                                                    false,
		"unexpected EOF":                                                         false,
	}
	for message, want := range cases {
		if got := IsDockerPushAuthorizationError(message); got != want {
			t.Errorf("IsDockerPushAuthorizationError(%q) = %v, want %v", message, got, want)
		}
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

func TestOrderedDockerBuildSpecsBuildsVersionArgBaseImagesBeforeDependents(t *testing.T) {
	workdir := t.TempDir()
	baseDir := filepath.Join(workdir, "erun-devops")
	apiDir := filepath.Join(workdir, "erun-backend-api")
	for _, dir := range []string{baseDir, apiDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(baseDir, "Dockerfile"), []byte("FROM alpine:3.22\n"), 0o644); err != nil {
		t.Fatalf("write base Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(apiDir, "Dockerfile"), []byte("ARG ERUN_VERSION\nFROM erunpaas/erun-devops:${ERUN_VERSION}\n"), 0o644); err != nil {
		t.Fatalf("write api Dockerfile: %v", err)
	}

	builds := []DockerBuildSpec{
		{
			ContextDir:     apiDir,
			DockerfilePath: filepath.Join(apiDir, "Dockerfile"),
			Image: DockerImageReference{
				Tag: "erunpaas/erun-backend-api:1.4.2",
			},
		},
		{
			ContextDir:     baseDir,
			DockerfilePath: filepath.Join(baseDir, "Dockerfile"),
			Image: DockerImageReference{
				Tag: "erunpaas/erun-devops:1.4.2",
			},
		},
	}

	ordered := orderedDockerBuildSpecs(builds)
	got := []string{ordered[0].Image.Tag, ordered[1].Image.Tag}
	want := []string{"erunpaas/erun-devops:1.4.2", "erunpaas/erun-backend-api:1.4.2"}
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
			return DockerBuildContext{Dir: projectRoot}, nil
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
			return DockerBuildContext{Dir: projectRoot}, nil
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
	secondDir := filepath.Join(projectRoot, "scripts", "zeta")
	writeBuildScriptForTest(t, firstDir)
	writeBuildScriptForTest(t, secondDir)

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
	requireNoError(t, err, "ResolveBuildExecution failed")

	var called bool
	ctx := Context{
		Logger: NewLoggerWithWriters(2, io.Discard, io.Discard),
		Stdin:  new(bytes.Buffer),
		Stdout: new(bytes.Buffer),
		Stderr: new(bytes.Buffer),
	}
	err = RunBuildExecution(ctx, execution, func(dir, path string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
		called = true
		requireCondition(t, dir == firstDir && path == "./build.sh", "unexpected script call: dir=%q path=%q", dir, path)
		requireEqual(t, len(env), 0, "script env count")
		return nil
	}, func(DockerBuildSpec, io.Writer, io.Writer) error {
		t.Fatal("unexpected docker build")
		return nil
	}, nil)
	requireNoError(t, err, "RunBuildExecution failed")
	requireCondition(t, called, "expected build script runner to be called")
}

func writeBuildScriptForTest(t *testing.T, dir string) {
	t.Helper()
	requireNoError(t, os.MkdirAll(dir, 0o755), "mkdir build script dir")
	requireNoError(t, os.WriteFile(filepath.Join(dir, "build.sh"), []byte("#!/bin/sh\n"), 0o755), "write build.sh")
}

func TestResolveBuildExecutionPrefersDockerBuildsOverNestedProjectBuildScript(t *testing.T) {
	projectRoot := t.TempDir()
	scriptDir := filepath.Join(projectRoot, "scripts", "alpha")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("mkdir script dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write build.sh: %v", err)
	}
	componentDir := filepath.Join(projectRoot, "tenant-a-devops", "docker", "tenant-a-devops")
	if err := os.MkdirAll(componentDir, 0o755); err != nil {
		t.Fatalf("mkdir component dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "tenant-a-devops", "VERSION"), []byte("1.2.3\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}

	execution, err := ResolveBuildExecution(
		ConfigStore{},
		func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		func() (DockerBuildContext, error) {
			return DockerBuildContext{Dir: projectRoot}, nil
		},
		time.Now,
		DockerCommandTarget{},
	)
	if err != nil {
		t.Fatalf("ResolveBuildExecution failed: %v", err)
	}
	if execution.script != nil {
		t.Fatalf("did not expect nested build script to override docker builds: %+v", execution.script)
	}
	if len(execution.dockerBuilds) != 1 {
		t.Fatalf("unexpected docker builds: %+v", execution.dockerBuilds)
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
	writeDockerBuildFixture(t, componentDirs[0])
	writeVersionFileForTest(t, filepath.Join(componentDirs[0], "VERSION"), "1.0.0")
	writeDockerBuildFixture(t, componentDirs[1])
	writeVersionFileForTest(t, filepath.Join(componentDirs[1], "VERSION"), "28.1.1")
	requireNoError(t, SaveProjectConfig(projectRoot, ProjectConfig{
		Environments: map[string]ProjectEnvironmentConfig{
			DefaultEnvironment: {ContainerRegistry: "erunpaas"},
		},
	}), "save project config")

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
	requireNoError(t, err, "ResolveBuildExecution failed")

	requireProjectRootDevopsBuilds(t, execution)
}

func requireProjectRootDevopsBuilds(t *testing.T, execution BuildExecutionSpec) {
	t.Helper()
	requireEqual(t, len(execution.dockerBuilds), 2, "docker build count")
	requireEqual(t, len(execution.dockerPushes), 0, "docker push count")
	buildTags := []string{execution.dockerBuilds[0].Image.Tag, execution.dockerBuilds[1].Image.Tag}
	requireContainsAllStrings(t, buildTags, []string{"erunpaas/tenant-a-devops:1.0.0", "erunpaas/erun-dind:28.1.1"})
}

func requireContainsAllStrings(t *testing.T, got, want []string) {
	t.Helper()
	for _, value := range want {
		requireCondition(t, containsString(got, value), "missing value %q in %+v", value, got)
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
	requireNoError(t, err, "ResolveBuildExecution failed")
	requireReleaseDockerBuildExecution(t, execution)
}

func requireReleaseDockerBuildExecution(t *testing.T, execution BuildExecutionSpec) {
	t.Helper()
	requireCondition(t, execution.release != nil, "expected release spec, got %+v", execution)
	requireEqual(t, execution.release.Version, "1.4.2", "release version")
	requireEqual(t, execution.release.NextVersion, "1.4.3", "next version")
	requireEqual(t, len(execution.dockerBuilds), 1, "docker build count")
	requireEqual(t, execution.dockerBuilds[0].Image.Tag, "ghcr.io/sophium/api:1.4.2", "docker build tag")
	requireMultiPlatformPushedBuild(t, execution.dockerBuilds[0])
	requireEqual(t, len(execution.dockerPushes), 1, "docker push count")
	requireEqual(t, execution.dockerPushes[0].Image.Tag, "ghcr.io/sophium/api:1.4.2", "docker push tag")
}

func requireMultiPlatformPushedBuild(t *testing.T, build DockerBuildSpec) {
	t.Helper()
	requireCondition(t, build.Push && reflect.DeepEqual(build.Platforms, []string{"linux/amd64", "linux/arm64"}), "expected multi-platform release build spec, got %+v", build)
}

func TestResolveBuildExecutionReleaseCarriesForceToReleaseSpec(t *testing.T) {
	projectRoot := setupReleaseProjectGitRepo(t, "main")

	execution, err := ResolveBuildExecution(
		ConfigStore{},
		func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		func() (DockerBuildContext, error) {
			return DockerBuildContext{Dir: projectRoot}, nil
		},
		nil,
		DockerCommandTarget{ProjectRoot: projectRoot, Environment: DefaultEnvironment, Release: true, Force: true},
	)
	if err != nil {
		t.Fatalf("ResolveBuildExecution failed: %v", err)
	}
	if execution.release == nil {
		t.Fatalf("expected release spec, got %+v", execution)
	}
	if !execution.release.Force {
		t.Fatalf("expected release force flag, got %+v", execution.release)
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
	if got := execution.dockerPushes[0].Image.Tag; got != "ghcr.io/sophium/api:1.4.2" {
		t.Fatalf("unexpected docker push tag: %q", got)
	}
}

func TestResolveBuildExecutionReleasePushesLocalDockerDependenciesAndDind(t *testing.T) {
	projectRoot := setupReleaseProjectGitRepo(t, "main")
	releaseRoot := filepath.Join(projectRoot, "erun-devops")

	apiDockerfilePath := filepath.Join(releaseRoot, "docker", "api", "Dockerfile")
	requireNoError(t, os.WriteFile(apiDockerfilePath, []byte("FROM ghcr.io/sophium/base:9.9.9\n"), 0o644), "write api Dockerfile")

	dindDir := filepath.Join(releaseRoot, "docker", "erun-dind")
	requireNoError(t, os.MkdirAll(dindDir, 0o755), "mkdir dind dir")
	requireNoError(t, os.WriteFile(filepath.Join(dindDir, "Dockerfile"), []byte("FROM docker:28.1.1-dind\n"), 0o644), "write dind Dockerfile")
	writeVersionFileForTest(t, filepath.Join(dindDir, "VERSION"), "28.1.1")

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
	requireNoError(t, err, "ResolveBuildExecution failed")

	requireReleaseDependencyPushes(t, execution)
}

func requireReleaseDependencyPushes(t *testing.T, execution BuildExecutionSpec) {
	t.Helper()
	wantTags := []string{"ghcr.io/sophium/api:1.4.2", "ghcr.io/sophium/base:9.9.9", "ghcr.io/sophium/erun-dind:28.1.1"}
	pushTags := make([]string, 0, len(execution.dockerPushes))
	for _, pushInput := range execution.dockerPushes {
		pushTags = append(pushTags, pushInput.Image.Tag)
	}
	requireContainsAllStrings(t, pushTags, wantTags)
	requireMultiPlatformReleaseBuilds(t, execution.dockerBuilds, wantTags)
}

func requireMultiPlatformReleaseBuilds(t *testing.T, builds []DockerBuildSpec, wantTags []string) {
	t.Helper()
	for _, build := range builds {
		if containsString(wantTags, build.Image.Tag) {
			requireMultiPlatformPushedBuild(t, build)
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

	if len(buildCalls) != 1 || buildCalls[0].Image.Tag != "ghcr.io/sophium/api:1.4.2" {
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
	requireNoError(t, os.WriteFile(filepath.Join(chartPath, "values.local.yaml"), nil, 0o644), "write values.local.yaml")
	requireNoError(t, SaveTenantConfig(TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: DefaultEnvironment,
	}), "save tenant config")
	requireNoError(t, SaveEnvConfig("tenant-a", EnvConfig{
		Name:              DefaultEnvironment,
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
	}), "save env config")
	requireNoError(t, SaveProjectConfig(projectRoot, ProjectConfig{}), "save project config")

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
	requireNoError(t, err, "ResolveBuildExecution failed")
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
	requireNoError(t, err, "ResolveDeploySpecForDockerTarget failed")
	requireReleaseCandidateVersion(t, deploySpec.Deploy.Version, "deploy version")

	stdout := new(bytes.Buffer)
	ctx := Context{
		Logger: NewLoggerWithWriters(2, stdout, io.Discard),
		DryRun: true,
		Stdin:  new(bytes.Buffer),
		Stdout: stdout,
		Stderr: io.Discard,
	}
	err = RunBuildExecutionAndDeploy(ctx, execution, []DeploySpec{deploySpec}, nil, nil, nil, func(HelmDeployParams) error {
		t.Fatal("unexpected deploy execution during dry-run")
		return nil
	})
	requireNoError(t, err, "RunBuildExecutionAndDeploy failed")

	output := strings.TrimSpace(stdout.String())
	requireStringContains(t, output, "release version: 1.4.2-rc.", "expected release version output")
	requireStringContains(t, output, "deployed version: 1.4.2-rc.", "expected deployed version output")
	lines := strings.Split(output, "\n")
	requireStringContains(t, lines[len(lines)-1], "deployed version: 1.4.2-rc.", "expected deployed version last")
}

func requireReleaseCandidateVersion(t *testing.T, version, label string) {
	t.Helper()
	requireCondition(t, version == "1.4.2-rc.0000000" || strings.HasPrefix(version, "1.4.2-rc."), "unexpected %s: %s", label, version)
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
