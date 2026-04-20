package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manifoldco/promptui"
	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

type dockerBuildCall struct {
	Dir            string
	DockerfilePath string
	Tag            string
	Platforms      []string
	Push           bool
	Stdout         io.Writer
	Stderr         io.Writer
}

type dockerPushCall struct {
	Tag    string
	Stdout io.Writer
	Stderr io.Writer
}

type dockerLoginCall struct {
	Registry string
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
}

type buildScriptCall struct {
	Dir    string
	Path   string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func buildCallFunc(run func(dockerBuildCall) error) common.DockerImageBuilderFunc {
	return func(buildInput common.DockerBuildSpec, stdout, stderr io.Writer) error {
		return run(dockerBuildCall{
			Dir:            buildInput.ContextDir,
			DockerfilePath: buildInput.DockerfilePath,
			Tag:            buildInput.Image.Tag,
			Platforms:      append([]string{}, buildInput.Platforms...),
			Push:           buildInput.Push,
			Stdout:         stdout,
			Stderr:         stderr,
		})
	}
}

func buildScriptCallFunc(run func(buildScriptCall) error) common.BuildScriptRunnerFunc {
	return func(dir, path string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
		return run(buildScriptCall{
			Dir:    dir,
			Path:   path,
			Env:    append([]string{}, env...),
			Stdin:  stdin,
			Stdout: stdout,
			Stderr: stderr,
		})
	}
}

func pushCallFunc(run func(dockerPushCall) error) common.DockerImagePusherFunc {
	return func(tag string, stdout, stderr io.Writer) error {
		return run(dockerPushCall{
			Tag:    tag,
			Stdout: stdout,
			Stderr: stderr,
		})
	}
}

func loginCallFunc(run func(dockerLoginCall) error) common.DockerRegistryLoginFunc {
	return func(registry string, stdin io.Reader, stdout, stderr io.Writer) error {
		return run(dockerLoginCall{
			Registry: registry,
			Stdin:    stdin,
			Stdout:   stdout,
			Stderr:   stderr,
		})
	}
}

func setupMultiDockerProject(t *testing.T, tenant string) (string, string, string, []string, []string) {
	t.Helper()

	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	moduleName := common.RuntimeReleaseName(tenant)
	moduleRoot := filepath.Join(projectRoot, moduleName)
	dockerDir := filepath.Join(moduleRoot, "docker")
	componentNames := []string{moduleName, "erun-dind", "erun-ubuntu"}
	componentDirs := make([]string, 0, len(componentNames))
	for _, componentName := range componentNames {
		componentDir := filepath.Join(dockerDir, componentName)
		if err := os.MkdirAll(componentDir, 0o755); err != nil {
			t.Fatalf("mkdir component dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(componentDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
			t.Fatalf("write Dockerfile: %v", err)
		}
		componentDirs = append(componentDirs, componentDir)
	}
	if err := os.WriteFile(filepath.Join(moduleRoot, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDirs[0], "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDirs[1], "VERSION"), []byte("28.1.1\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDirs[2], "VERSION"), []byte("noble-20260217\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               tenant,
		ProjectRoot:        projectRoot,
		DefaultEnvironment: common.DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	tags := []string{
		"erunpaas/" + componentNames[0] + ":1.0.0",
		"erunpaas/erun-dind:28.1.1",
		"erunpaas/erun-ubuntu:noble-20260217",
	}
	return projectRoot, moduleRoot, dockerDir, componentDirs, tags
}

func assertTagSet(t *testing.T, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("unexpected tag count: got %v want %v", got, want)
	}

	gotCounts := make(map[string]int, len(got))
	for _, tag := range got {
		gotCounts[tag]++
	}
	for _, tag := range want {
		if gotCounts[tag] == 0 {
			t.Fatalf("missing expected tag %q in %v", tag, got)
		}
		gotCounts[tag]--
	}
	for tag, count := range gotCounts {
		if count != 0 {
			t.Fatalf("unexpected extra tag %q in %v", tag, got)
		}
	}
}

func TestNewRootCmdRegistersDevopsContainerBuildCommand(t *testing.T) {
	cmd := newTestRootCmd(testRootDeps{
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: t.TempDir()}, nil
		},
	})

	found, _, err := cmd.Find([]string{"devops", "container", "build"})
	if err != nil {
		t.Fatalf("Find(devops container build) failed: %v", err)
	}
	if found == nil || found.Name() != "build" || found.Parent() == nil || found.Parent().Name() != "container" {
		t.Fatalf("expected devops container build command to be registered, got %+v", found)
	}
}

func TestNewRootCmdRegistersDevopsContainerPushCommand(t *testing.T) {
	cmd := newTestRootCmd(testRootDeps{
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: t.TempDir()}, nil
		},
	})

	found, _, err := cmd.Find([]string{"devops", "container", "push"})
	if err != nil {
		t.Fatalf("Find(devops container push) failed: %v", err)
	}
	if found == nil || found.Name() != "push" || found.Parent() == nil || found.Parent().Name() != "container" {
		t.Fatalf("expected devops container push command to be registered, got %+v", found)
	}
}

func TestNewRootCmdRegistersBuildShorthandWhenDockerfilePresent(t *testing.T) {
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", t.TempDir(), nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			dir := t.TempDir()
			return common.DockerBuildContext{
				Dir:            dir,
				DockerfilePath: filepath.Join(dir, "Dockerfile"),
			}, nil
		},
	})

	if !hasSubcommand(cmd, "build") {
		t.Fatal("expected build shorthand command to be registered")
	}
	if !hasSubcommand(cmd, "push") {
		t.Fatal("expected push shorthand command to be registered")
	}

	buildCmd, _, err := cmd.Find([]string{"build"})
	if err != nil {
		t.Fatalf("Find(build) failed: %v", err)
	}
	if buildCmd.Short != "Build the container image in the current directory" {
		t.Fatalf("unexpected build short help: %q", buildCmd.Short)
	}

	pushCmd, _, err := cmd.Find([]string{"push"})
	if err != nil {
		t.Fatalf("Find(push) failed: %v", err)
	}
	if pushCmd.Short != "Build and push the current container image" {
		t.Fatalf("unexpected push short help: %q", pushCmd.Short)
	}
}

func TestNewRootCmdRegistersBuildShorthandWhenProjectBuildScriptPresent(t *testing.T) {
	projectRoot := t.TempDir()
	scriptDir := filepath.Join(projectRoot, "scripts", "build")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("mkdir script dir: %v", err)
	}
	scriptPath := filepath.Join(scriptDir, "build.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write build.sh: %v", err)
	}

	cmd := newTestRootCmd(testRootDeps{
		OptionalBuildFindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: projectRoot}, nil
		},
	})

	if !hasSubcommand(cmd, "build") {
		t.Fatal("expected build shorthand command to be registered")
	}
	if hasSubcommand(cmd, "push") {
		t.Fatal("did not expect push shorthand command to be registered")
	}

	buildCmd, _, err := cmd.Find([]string{"build"})
	if err != nil {
		t.Fatalf("Find(build) failed: %v", err)
	}
	if buildCmd.Short != "Build the project" {
		t.Fatalf("unexpected build short help: %q", buildCmd.Short)
	}
}

func TestNewRootCmdRegistersBuildShorthandWhenDockerDirectoryContainsDockerfiles(t *testing.T) {
	dockerDir := filepath.Join(t.TempDir(), "erun-devops", "docker")
	if err := os.MkdirAll(filepath.Join(dockerDir, "erun-devops"), 0o755); err != nil {
		t.Fatalf("mkdir component dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dockerDir, "erun-dind"), 0o755); err != nil {
		t.Fatalf("mkdir component dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dockerDir, "erun-devops", "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dockerDir, "erun-dind", "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}

	cmd := newTestRootCmd(testRootDeps{
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: dockerDir}, nil
		},
	})

	if !hasSubcommand(cmd, "build") {
		t.Fatal("expected build shorthand command to be registered")
	}
	if hasSubcommand(cmd, "push") {
		t.Fatal("did not expect push shorthand command to be registered")
	}

	buildCmd, _, err := cmd.Find([]string{"build"})
	if err != nil {
		t.Fatalf("Find(build) failed: %v", err)
	}
	if buildCmd.Short != "Build and push the devops container images for the current project" {
		t.Fatalf("unexpected build short help: %q", buildCmd.Short)
	}

	if hasSubcommand(cmd, "push") {
		t.Fatal("did not expect push shorthand command to be registered")
	}
}

func TestNewRootCmdRegistersContextAwareShorthandHelpWhenCurrentDirectoryIsProjectRoot(t *testing.T) {
	projectRoot, _, _, _, _ := setupMultiDockerProject(t, "tenant-a")

	cmd := newTestRootCmd(testRootDeps{
		OptionalBuildFindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: projectRoot}, nil
		},
	})

	buildCmd, _, err := cmd.Find([]string{"build"})
	if err != nil {
		t.Fatalf("Find(build) failed: %v", err)
	}
	if buildCmd.Short != "Build and push the project" {
		t.Fatalf("unexpected build short help: %q", buildCmd.Short)
	}

	if hasSubcommand(cmd, "push") {
		t.Fatal("did not expect push shorthand command to be registered")
	}
}

func TestNewRootCmdOmitsBuildShorthandWhenDockerfileAbsent(t *testing.T) {
	cmd := newTestRootCmd(testRootDeps{
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: t.TempDir()}, nil
		},
	})

	if hasSubcommand(cmd, "build") {
		t.Fatal("did not expect build shorthand command to be registered")
	}
	if hasSubcommand(cmd, "push") {
		t.Fatal("did not expect push shorthand command to be registered")
	}
}

func TestRootBuildShorthandRunsDockerBuild(t *testing.T) {
	projectRoot := t.TempDir()
	workdir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-ubuntu")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("noble-20260217\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}
	buildContext := common.DockerBuildContext{
		Dir:            workdir,
		DockerfilePath: filepath.Join(workdir, "Dockerfile"),
	}

	var received dockerBuildCall
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return buildContext, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			received = req
			return nil
		}),
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			t.Fatalf("unexpected push request: %+v", req)
			return nil
		}),
		LaunchShell: func(req common.ShellLaunchParams) error {
			t.Fatalf("unexpected shell launch: %+v", req)
			return nil
		},
	})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"build"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if received.Dir != projectRoot || received.DockerfilePath != buildContext.DockerfilePath {
		t.Fatalf("unexpected build request: %+v", received)
	}
	if received.Tag != "erunpaas/erun-ubuntu:noble-20260217" {
		t.Fatalf("unexpected image tag: %+v", received)
	}
	if received.Stdout != stdout || received.Stderr != stderr {
		t.Fatalf("unexpected output writers: %+v", received)
	}
}

func TestRootBuildShorthandPrefersDockerBuildOverNestedProjectBuildScript(t *testing.T) {
	projectRoot := t.TempDir()
	scriptDir := filepath.Join(projectRoot, "scripts", "build")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("mkdir script dir: %v", err)
	}
	scriptPath := filepath.Join(scriptDir, "build.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write build.sh: %v", err)
	}

	buildDir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	var received dockerBuildCall
	cmd := newTestRootCmd(testRootDeps{
		OptionalBuildFindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            buildDir,
				DockerfilePath: filepath.Join(buildDir, "Dockerfile"),
			}, nil
		},
		RunBuildScript: buildScriptCallFunc(func(req buildScriptCall) error {
			t.Fatalf("unexpected build script call: %+v", req)
			return nil
		}),
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			received = req
			return nil
		}),
	})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"build"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if received.Dir != projectRoot || received.DockerfilePath != filepath.Join(buildDir, "Dockerfile") {
		t.Fatalf("unexpected docker build call: %+v", received)
	}
	if received.Stdout != stdout || received.Stderr != stderr {
		t.Fatalf("unexpected output writers: %+v", received)
	}
}

func TestRootBuildShorthandDeployRejectsProjectBuildScript(t *testing.T) {
	projectRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(projectRoot, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write build.sh: %v", err)
	}

	buildDir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}

	cmd := newTestRootCmd(testRootDeps{
		OptionalBuildFindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            buildDir,
				DockerfilePath: filepath.Join(buildDir, "Dockerfile"),
			}, nil
		},
	})
	cmd.SetArgs([]string{"build", "--deploy"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected build --deploy to reject project build scripts")
	}
	if err.Error() != "build deploy is not supported for project build scripts" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRootBuildShorthandBuildsAllDockerImagesWhenCurrentDirectoryIsDockerDirectory(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	dockerDir := filepath.Join(projectRoot, "erun-devops", "docker")
	componentDirs := []string{
		filepath.Join(dockerDir, "erun-devops"),
		filepath.Join(dockerDir, "erun-dind"),
		filepath.Join(dockerDir, "erun-ubuntu"),
	}
	for _, dir := range componentDirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir component dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
			t.Fatalf("write Dockerfile: %v", err)
		}
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "erun",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: common.DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDirs[0], "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDirs[1], "VERSION"), []byte("28.1.1\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(componentDirs[2], "VERSION"), []byte("noble-20260217\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}

	var built []dockerBuildCall
	var pushed []dockerPushCall
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: dockerDir}, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			built = append(built, req)
			return nil
		}),
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			pushed = append(pushed, req)
			return nil
		}),
	})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"build"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(built) != 3 || len(pushed) != 0 {
		t.Fatalf("unexpected build/push counts: builds=%d pushes=%d", len(built), len(pushed))
	}

	gotTags := []string{built[0].Tag, built[1].Tag, built[2].Tag}
	wantTags := []string{
		"erunpaas/erun-devops:1.0.0",
		"erunpaas/erun-dind:28.1.1",
		"erunpaas/erun-ubuntu:noble-20260217",
	}
	for i := range wantTags {
		if gotTags[i] != wantTags[i] {
			t.Fatalf("unexpected image tags: got %v want %v", gotTags, wantTags)
		}
	}
	for _, req := range built {
		if req.Dir != projectRoot {
			t.Fatalf("expected project root build context, got %+v", req)
		}
		if req.Stdout != stdout || req.Stderr != stderr {
			t.Fatalf("unexpected output writers: %+v", req)
		}
	}
}

func TestRootBuildShorthandBuildsAllDockerImagesWhenCurrentDirectoryIsProjectRoot(t *testing.T) {
	projectRoot, _, _, _, wantTags := setupMultiDockerProject(t, "tenant-a")

	var built []dockerBuildCall
	var pushed []dockerPushCall
	cmd := newTestRootCmd(testRootDeps{
		OptionalBuildFindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: projectRoot}, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			built = append(built, req)
			return nil
		}),
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			pushed = append(pushed, req)
			return nil
		}),
	})
	cmd.SetArgs([]string{"build"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if len(built) != len(wantTags) || len(pushed) != 0 {
		t.Fatalf("unexpected build/push counts: builds=%d pushes=%d", len(built), len(pushed))
	}
	gotTags := make([]string, 0, len(built))
	for i, req := range built {
		if req.Dir != projectRoot {
			t.Fatalf("unexpected build request %d: %+v", i, req)
		}
		gotTags = append(gotTags, req.Tag)
	}
	assertTagSet(t, gotTags, wantTags)
}

func TestRootBuildShorthandBuildsAllDockerImagesWhenCurrentDirectoryIsDevopsModuleRoot(t *testing.T) {
	projectRoot, moduleRoot, _, _, wantTags := setupMultiDockerProject(t, "tenant-a")

	var built []dockerBuildCall
	var pushed []dockerPushCall
	cmd := newTestRootCmd(testRootDeps{
		OptionalBuildFindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: moduleRoot}, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			built = append(built, req)
			return nil
		}),
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			pushed = append(pushed, req)
			return nil
		}),
	})
	cmd.SetArgs([]string{"build"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if len(built) != len(wantTags) || len(pushed) != 0 {
		t.Fatalf("unexpected build/push counts: builds=%d pushes=%d", len(built), len(pushed))
	}
	gotTags := make([]string, 0, len(built))
	for i, req := range built {
		if req.Dir != projectRoot {
			t.Fatalf("unexpected build request %d: %+v", i, req)
		}
		gotTags = append(gotTags, req.Tag)
	}
	assertTagSet(t, gotTags, wantTags)
}

func TestRootBuildShorthandUsesExactVersionFromCurrentBuildDirectoryForLocalEnvironment(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	workdir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "erun",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: common.DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	fixedNow := time.Date(2026, time.April, 6, 12, 34, 56, 0, time.UTC)
	var received dockerBuildCall
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			received = req
			return nil
		}),
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			t.Fatalf("unexpected push request: %+v", req)
			return nil
		}),
		Now: func() time.Time {
			return fixedNow
		},
	})
	cmd.SetArgs([]string{"build"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	wantVersion := "1.1.0"
	if received.Tag != "erunpaas/erun-devops:"+wantVersion {
		t.Fatalf("unexpected image tag: %+v", received)
	}
}

func TestRootBuildShorthandDryRunPrintsCommandWithoutExecuting(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	workdir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "erun",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: common.DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	stderr := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			t.Fatalf("unexpected build request during dry-run: %+v", req)
			return nil
		}),
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			t.Fatalf("unexpected push request during dry-run: %+v", req)
			return nil
		}),
		Now: func() time.Time {
			return time.Date(2026, time.April, 6, 13, 16, 30, 0, time.UTC)
		},
	})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"build", "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if got := stderr.String(); !bytes.Contains([]byte(got), []byte("docker build -t erunpaas/erun-devops:1.1.0")) {
		t.Fatalf("expected dry-run trace output, got %q", got)
	}
	if strings.Contains(stderr.String(), "docker push erunpaas/erun-devops:1.1.0") {
		t.Fatalf("did not expect push dry-run trace, got %q", stderr.String())
	}
}

func TestRootBuildShorthandDryRunPrintsBuildCommandsForProjectRoot(t *testing.T) {
	projectRoot, _, _, _, wantTags := setupMultiDockerProject(t, "tenant-a")

	stderr := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{
		OptionalBuildFindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: projectRoot}, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			t.Fatalf("unexpected build request during dry-run: %+v", req)
			return nil
		}),
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			t.Fatalf("unexpected push request during dry-run: %+v", req)
			return nil
		}),
	})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"build", "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stderr.String()
	for _, tag := range wantTags {
		if !strings.Contains(output, "docker build -t "+tag) {
			t.Fatalf("expected dry-run build trace for %q, got %q", tag, output)
		}
		if strings.Contains(output, "docker push "+tag) {
			t.Fatalf("did not expect dry-run push trace for %q, got %q", tag, output)
		}
	}
}

func TestRootBuildShorthandDryRunPrintsBuildScriptCommandWithoutExecuting(t *testing.T) {
	projectRoot := t.TempDir()
	scriptDir := filepath.Join(projectRoot, "scripts", "build")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("mkdir script dir: %v", err)
	}
	scriptPath := filepath.Join(scriptDir, "build.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write build.sh: %v", err)
	}

	stderr := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{
		OptionalBuildFindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: projectRoot}, nil
		},
		RunBuildScript: buildScriptCallFunc(func(req buildScriptCall) error {
			t.Fatalf("unexpected build script execution during dry-run: %+v", req)
			return nil
		}),
	})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"build", "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if got := stderr.String(); !strings.Contains(got, "cd "+scriptDir+" && ./build.sh") {
		t.Fatalf("expected build.sh dry-run trace, got %q", got)
	}
}

func TestRootBuildShorthandIgnoresBuildScriptInDockerArtifactDirectory(t *testing.T) {
	projectRoot := t.TempDir()
	buildDir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write artifact build.sh: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	var built dockerBuildCall
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            buildDir,
				DockerfilePath: filepath.Join(buildDir, "Dockerfile"),
			}, nil
		},
		RunBuildScript: buildScriptCallFunc(func(req buildScriptCall) error {
			t.Fatalf("unexpected build script call: %+v", req)
			return nil
		}),
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			built = req
			return nil
		}),
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			t.Fatalf("unexpected push request: %+v", req)
			return nil
		}),
	})
	cmd.SetArgs([]string{"build"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if built.Dir != projectRoot || built.Tag != "erunpaas/erun-devops:1.1.0" {
		t.Fatalf("unexpected docker build request: %+v", built)
	}
}

func TestRootBuildShorthandVerbosePrintsTraceBeforeExecuting(t *testing.T) {
	projectRoot := t.TempDir()
	workdir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	stderr := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			return nil
		}),
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			t.Fatalf("unexpected push request: %+v", req)
			return nil
		}),
	})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"-v", "build"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if got := stderr.String(); !strings.Contains(got, "docker build -t erunpaas/erun-devops:1.1.0") {
		t.Fatalf("expected verbose build trace, got %q", got)
	}
	if got := stderr.String(); strings.Contains(got, "decision:") {
		t.Fatalf("did not expect decision notes at -v, got %q", got)
	}
}

func TestRootBuildShorthandDoubleVerbosePrintsBuildTraceBeforeExecuting(t *testing.T) {
	projectRoot := t.TempDir()
	workdir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	stderr := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			return nil
		}),
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			t.Fatalf("unexpected push request: %+v", req)
			return nil
		}),
	})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"-vv", "build"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	got := stderr.String()
	if !strings.Contains(got, "docker build -t erunpaas/erun-devops:1.1.0") {
		t.Fatalf("expected verbose build trace, got %q", got)
	}
	if strings.Contains(got, "decision:") {
		t.Fatalf("did not expect decision notes at -vv, got %q", got)
	}
}

func TestDevopsContainerBuildFailsWithoutDockerfile(t *testing.T) {
	cmd := newTestRootCmd(testRootDeps{
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: t.TempDir()}, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			t.Fatalf("unexpected build request: %+v", req)
			return nil
		}),
	})
	cmd.SetArgs([]string{"devops", "container", "build"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected missing Dockerfile error")
	}
	if got := err.Error(); got != "dockerfile not found in current directory" {
		t.Fatalf("unexpected error: %q", got)
	}
}

func TestDevopsContainerPushUsesResolvedImageTag(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	workdir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "erun",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: common.DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	var built dockerBuildCall
	var received dockerPushCall
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			built = req
			return nil
		}),
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			received = req
			return nil
		}),
	})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"devops", "container", "push"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if built.Tag != "erunpaas/erun-devops:1.1.0" {
		t.Fatalf("unexpected build tag: %+v", built)
	}
	if received.Tag != "erunpaas/erun-devops:1.1.0" {
		t.Fatalf("unexpected image tag: %+v", received)
	}
	if received.Stdout != stdout || received.Stderr != stderr {
		t.Fatalf("unexpected output writers: %+v", received)
	}
}

func TestDevopsContainerPushPromptsLoginAndRetriesOnAuthError(t *testing.T) {
	projectRoot := t.TempDir()
	workdir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	pushCalls := 0
	loginRegistry := "unexpected"
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		SelectRunner: func(prompt promptui.Select) (int, string, error) {
			return 0, loginAndRetryPushOption, nil
		},
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			pushCalls++
			if pushCalls == 1 {
				return common.DockerRegistryAuthError{
					Tag:      req.Tag,
					Registry: "",
					Message:  "push access denied: insufficient_scope: authorization failed",
					Err:      errors.New("exit status 1"),
				}
			}
			return nil
		}),
		LoginToDockerRegistry: loginCallFunc(func(req dockerLoginCall) error {
			loginRegistry = req.Registry
			return nil
		}),
	})
	cmd.SetArgs([]string{"devops", "container", "push"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if pushCalls != 2 {
		t.Fatalf("expected push retry, got %d push calls", pushCalls)
	}
	if loginRegistry != "" {
		t.Fatalf("expected Docker Hub login, got %q", loginRegistry)
	}
}

func TestBuildCommandReleasePromptsLoginAndRetriesOnAuthError(t *testing.T) {
	projectRoot := createReleaseGitRepo(t, "main")
	remoteRoot := filepath.Join(t.TempDir(), "release-remote.git")
	if err := os.MkdirAll(remoteRoot, 0o755); err != nil {
		t.Fatalf("mkdir remote root: %v", err)
	}
	runGitCommand(t, remoteRoot, "init", "--bare")
	runGitCommand(t, projectRoot, "remote", "add", "origin", remoteRoot)
	runGitCommand(t, projectRoot, "push", "-u", "origin", "main")
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	runGitCommand(t, projectRoot, "add", ".")
	runGitCommand(t, projectRoot, "commit", "-m", "configure registry")
	runGitCommand(t, projectRoot, "push", "origin", "main")

	pushBuildCalls := 0
	loginRegistry := "unexpected"
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		OptionalBuildFindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: projectRoot}, nil
		},
		SelectRunner: func(prompt promptui.Select) (int, string, error) {
			return 0, loginAndRetryPushOption, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			if req.Push {
				pushBuildCalls++
				if pushBuildCalls == 1 {
					return common.DockerRegistryAuthError{
						Tag:      req.Tag,
						Registry: "",
						Message:  "push access denied: insufficient_scope: authorization failed",
						Err:      errors.New("exit status 1"),
					}
				}
			}
			return nil
		}),
		LoginToDockerRegistry: loginCallFunc(func(req dockerLoginCall) error {
			loginRegistry = req.Registry
			return nil
		}),
	})
	cmd.SetArgs([]string{"build", "--release"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if pushBuildCalls != 2 {
		t.Fatalf("expected release build retry, got %d pushed build calls", pushBuildCalls)
	}
	if loginRegistry != "" {
		t.Fatalf("expected Docker Hub login, got %q", loginRegistry)
	}
}

func TestRootPushShorthandUsesResolvedImageTag(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	workdir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "erun",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: common.DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	var built dockerBuildCall
	var received dockerPushCall
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			built = req
			return nil
		}),
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			received = req
			return nil
		}),
	})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"push"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if built.Tag != "erunpaas/erun-devops:1.1.0" {
		t.Fatalf("unexpected build tag: %+v", built)
	}
	if received.Tag != "erunpaas/erun-devops:1.1.0" {
		t.Fatalf("unexpected image tag: %+v", received)
	}
	if received.Stdout != stdout || received.Stderr != stderr {
		t.Fatalf("unexpected output writers: %+v", received)
	}
}

func TestRootPushShorthandBuildsAndPushesSameExactVersionFromCurrentBuildDirectoryForLocalEnvironment(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	workdir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "erun",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: common.DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}

	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	var built dockerBuildCall
	var received dockerPushCall
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			built = req
			return nil
		}),
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			received = req
			return nil
		}),
		Now: func() time.Time {
			return time.Date(2026, time.April, 6, 14, 0, 0, 0, time.UTC)
		},
	})
	cmd.SetArgs([]string{"push"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if built.Tag != "erunpaas/erun-devops:1.1.0" {
		t.Fatalf("unexpected build tag: %+v", built)
	}
	if received.Tag != "erunpaas/erun-devops:1.1.0" {
		t.Fatalf("unexpected image tag: %+v", received)
	}
}

func TestDevopsContainerPushFailsWhenCurrentDirectoryIsProjectRoot(t *testing.T) {
	projectRoot, _, _, _, wantTags := setupMultiDockerProject(t, "tenant-a")

	cmd := newTestRootCmd(testRootDeps{
		OptionalBuildFindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: projectRoot}, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			t.Fatalf("unexpected build request: %+v", req)
			return nil
		}),
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			t.Fatalf("unexpected push request: %+v", req)
			return nil
		}),
	})
	cmd.SetArgs([]string{"devops", "container", "push"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected missing Dockerfile error")
	}
	if got := err.Error(); got != "dockerfile not found in current directory" {
		t.Fatalf("unexpected error: %q want tags=%v", got, wantTags)
	}
}

func TestDevopsContainerPushFailsWhenCurrentDirectoryIsDevopsModuleRoot(t *testing.T) {
	projectRoot, moduleRoot, _, _, wantTags := setupMultiDockerProject(t, "tenant-a")

	cmd := newTestRootCmd(testRootDeps{
		OptionalBuildFindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: moduleRoot}, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			t.Fatalf("unexpected build request: %+v", req)
			return nil
		}),
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			t.Fatalf("unexpected push request: %+v", req)
			return nil
		}),
	})
	cmd.SetArgs([]string{"devops", "container", "push"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected missing Dockerfile error")
	}
	if got := err.Error(); got != "dockerfile not found in current directory" {
		t.Fatalf("unexpected error: %q want tags=%v", got, wantTags)
	}
}

func TestDevopsContainerPushFailsWhenCurrentDirectoryIsDockerDirectory(t *testing.T) {
	projectRoot, _, dockerDir, _, wantTags := setupMultiDockerProject(t, "tenant-a")

	cmd := newTestRootCmd(testRootDeps{
		OptionalBuildFindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: dockerDir}, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			t.Fatalf("unexpected build request: %+v", req)
			return nil
		}),
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			t.Fatalf("unexpected push request: %+v", req)
			return nil
		}),
	})
	cmd.SetArgs([]string{"devops", "container", "push"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected missing Dockerfile error")
	}
	if got := err.Error(); got != "dockerfile not found in current directory" {
		t.Fatalf("unexpected error: %q want tags=%v", got, wantTags)
	}
}

func TestRootBuildShorthandUsesSnapshotWhenVersionIsInheritedFromParentModule(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	workdir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "erun",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: common.DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}

	fixedNow := time.Date(2026, time.April, 6, 12, 34, 56, 0, time.UTC)
	var received dockerBuildCall
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			received = req
			return nil
		}),
		Now: func() time.Time {
			return fixedNow
		},
	})
	cmd.SetArgs([]string{"build"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if received.Tag != "erunpaas/erun-devops:1.0.0-snapshot-20260406123456" {
		t.Fatalf("unexpected image tag: %+v", received)
	}
}

func TestRootPushShorthandDryRunPrintsCommandWithoutExecuting(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	workdir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "erun",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: common.DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	stderr := new(bytes.Buffer)
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			t.Fatalf("unexpected build request during dry-run: %+v", req)
			return nil
		}),
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			t.Fatalf("unexpected push request during dry-run: %+v", req)
			return nil
		}),
	})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"push", "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	got := stderr.String()
	if !bytes.Contains([]byte(got), []byte("docker build -t erunpaas/erun-devops:1.1.0")) {
		t.Fatalf("expected dry-run build trace output, got %q", got)
	}
	if !bytes.Contains([]byte(got), []byte("--build-arg ERUN_VERSION=1.1.0")) {
		t.Fatalf("expected dry-run build arg output, got %q", got)
	}
	if !bytes.Contains([]byte(got), []byte("docker push erunpaas/erun-devops:1.1.0")) {
		t.Fatalf("expected dry-run trace output, got %q", got)
	}
}

func TestDevopsContainerPushFailsWithoutDockerfile(t *testing.T) {
	cmd := newTestRootCmd(testRootDeps{
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: t.TempDir()}, nil
		},
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			t.Fatalf("unexpected push request: %+v", req)
			return nil
		}),
	})
	cmd.SetArgs([]string{"devops", "container", "push"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected missing Dockerfile error")
	}
	if got := err.Error(); got != "dockerfile not found in current directory" {
		t.Fatalf("unexpected error: %q", got)
	}
}

func TestDevopsContainerPushReturnsOriginalAuthErrorWhenLoginCancelled(t *testing.T) {
	projectRoot := t.TempDir()
	workdir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	expectedErr := common.DockerRegistryAuthError{
		Tag:      "erunpaas/erun-devops:1.1.0",
		Registry: "",
		Message:  "push access denied: insufficient_scope: authorization failed",
		Err:      errors.New("exit status 1"),
	}

	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		SelectRunner: func(prompt promptui.Select) (int, string, error) {
			return 1, cancelPushOption, nil
		},
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			return expectedErr
		}),
		LoginToDockerRegistry: loginCallFunc(func(req dockerLoginCall) error {
			t.Fatalf("unexpected login request: %+v", req)
			return nil
		}),
	})
	cmd.SetArgs([]string{"devops", "container", "push"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected auth error")
	}
	if got := err.Error(); got != expectedErr.Error() {
		t.Fatalf("unexpected error: %q", got)
	}
}

func TestRootCommandTreatsBuildAsEnvironmentWhenDockerfileAbsent(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "tenant-a-build")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "build",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{Name: "build", RepoPath: projectRoot, KubernetesContext: "cluster-build"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	launched := common.ShellLaunchParams{}
	cmd := newTestRootCmd(testRootDeps{
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: t.TempDir()}, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			t.Fatalf("unexpected build request: %+v", req)
			return nil
		}),
		LaunchShell: func(req common.ShellLaunchParams) error {
			launched = req
			return nil
		},
	})
	cmd.SetArgs([]string{"build"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if launched.Dir != projectRoot || launched.Title != "tenant-a-build" {
		t.Fatalf("unexpected shell launch: %+v", launched)
	}
}

func TestRootCommandTreatsPushAsEnvironmentWhenDockerfileAbsent(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := filepath.Join(t.TempDir(), "tenant-a-push")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatalf("mkdir project root: %v", err)
	}
	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "push",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{Name: "push", RepoPath: projectRoot, KubernetesContext: "cluster-push"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	launched := common.ShellLaunchParams{}
	cmd := newTestRootCmd(testRootDeps{
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: t.TempDir()}, nil
		},
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			t.Fatalf("unexpected push request: %+v", req)
			return nil
		}),
		LaunchShell: func(req common.ShellLaunchParams) error {
			launched = req
			return nil
		},
	})
	cmd.SetArgs([]string{"push"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if launched.Dir != projectRoot || launched.Title != "tenant-a-push" {
		t.Fatalf("unexpected shell launch: %+v", launched)
	}
}

func TestRunContainerBuildCommandPropagatesBuildContextErrors(t *testing.T) {
	expectedErr := errors.New("resolve failed")
	cmd := newBuildCmd(
		common.ConfigStore{},
		nil,
		func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{}, expectedErr
		},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected %v, got %v", expectedErr, err)
	}
}

func TestRunContainerPushCommandPropagatesBuildContextErrors(t *testing.T) {
	expectedErr := errors.New("resolve failed")
	containerCmd := newCommandGroup(
		"container",
		"Container utilities",
		newBuildCmd(common.ConfigStore{}, nil, func() (common.DockerBuildContext, error) {
			t.Fatal("unexpected build execution")
			return common.DockerBuildContext{}, nil
		}, nil, nil, nil, nil, nil, nil, nil, nil),
		newPushCmd(common.ConfigStore{}, nil, func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{}, expectedErr
		}, nil, nil, nil),
	)
	k8sCmd := newCommandGroup(
		"k8s",
		"Kubernetes utilities",
		newK8sDeployCmd(common.ConfigStore{}, nil, nil, nil, nil, nil, nil, func(common.HelmDeployParams) error {
			t.Fatal("unexpected deploy execution")
			return nil
		}),
	)
	cmd := newCommandGroup("devops", "DevOps utilities", containerCmd, k8sCmd)
	cmd.SetArgs([]string{"container", "push"})

	err := cmd.Execute()
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected %v, got %v", expectedErr, err)
	}
}

func TestResolveDockerBuildTagPrefersCurrentDirectoryVersion(t *testing.T) {
	projectRoot := t.TempDir()
	buildDir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-ubuntu")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("registry.example/team")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "VERSION"), []byte("noble-20260217\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	imageRef, err := func(buildDir string, target common.DockerCommandTarget) (common.DockerImageReference, error) {
		return common.ResolveDockerImageReference(
			common.ConfigStore{},
			func() (string, string, error) {
				return "erun", projectRoot, nil
			},
			common.ResolveDockerBuildContext,
			time.Now,
			buildDir,
			target,
		)
	}(buildDir, common.DockerCommandTarget{})
	if err != nil {
		t.Fatalf("ResolveDockerImageReference failed: %v", err)
	}

	tag := imageRef.Tag
	if tag != "registry.example/team/erun-ubuntu:noble-20260217" {
		t.Fatalf("unexpected tag: %q", tag)
	}
}

func TestResolveDockerBuildTagUsesDefaultEnvironmentRegistry(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	buildDir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-ubuntu")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "erun",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "prod",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, common.ProjectConfig{
		Environments: map[string]common.ProjectEnvironmentConfig{
			"local": {ContainerRegistry: "local-registry"},
			"prod":  {ContainerRegistry: "registry.example/team"},
		},
	}); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "VERSION"), []byte("noble-20260217\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	imageRef, err := func(buildDir string, target common.DockerCommandTarget) (common.DockerImageReference, error) {
		return common.ResolveDockerImageReference(
			common.ConfigStore{},
			func() (string, string, error) {
				return "erun", projectRoot, nil
			},
			common.ResolveDockerBuildContext,
			time.Now,
			buildDir,
			target,
		)
	}(buildDir, common.DockerCommandTarget{})
	if err != nil {
		t.Fatalf("ResolveDockerImageReference failed: %v", err)
	}

	tag := imageRef.Tag
	if tag != "registry.example/team/erun-ubuntu:noble-20260217" {
		t.Fatalf("unexpected tag: %q", tag)
	}
}

func TestResolveDockerBuildTagUsesVersionOverrideInLocal(t *testing.T) {
	projectRoot := t.TempDir()
	buildDir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("registry.example/team")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}

	imageRef, err := common.ResolveDockerImageReference(
		common.ConfigStore{},
		func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		common.ResolveDockerBuildContext,
		time.Now,
		buildDir,
		common.DockerCommandTarget{VersionOverride: "1.0.0-pr.abc1234"},
	)
	if err != nil {
		t.Fatalf("ResolveDockerImageReference failed: %v", err)
	}

	if got := imageRef.Tag; got != "registry.example/team/erun-devops:1.0.0-pr.abc1234" {
		t.Fatalf("unexpected tag: %q", got)
	}
}

func TestResolveDockerBuildTagKeepsBuildDirVersionWhenOverrideIsSet(t *testing.T) {
	projectRoot := t.TempDir()
	buildDir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-ubuntu")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("registry.example/team")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "VERSION"), []byte("noble-20260217\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	imageRef, err := common.ResolveDockerImageReference(
		common.ConfigStore{},
		func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		common.ResolveDockerBuildContext,
		time.Now,
		buildDir,
		common.DockerCommandTarget{VersionOverride: "1.0.0-pr.abc1234"},
	)
	if err != nil {
		t.Fatalf("ResolveDockerImageReference failed: %v", err)
	}

	if got := imageRef.Tag; got != "registry.example/team/erun-ubuntu:noble-20260217" {
		t.Fatalf("unexpected tag: %q", got)
	}
}

func TestBuildCommandVersionOverrideAvoidsSnapshotTag(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	buildDir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}

	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContextAtDir(buildDir)
		},
		BuildDockerImage: func(buildInput common.DockerBuildSpec, stdout, stderr io.Writer) error {
			t.Fatalf("unexpected build execution: %s", buildInput.Image.Tag)
			return nil
		},
	})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"build", "--dry-run", "--version", "1.0.0-pr.abc1234"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if got := stderr.String(); !strings.Contains(got, "erun-devops:1.0.0-pr.abc1234") || strings.Contains(got, "-snapshot-") {
		t.Fatalf("unexpected dry-run output:\n%s", got)
	}
}

func TestBuildCommandVersionOverrideDoesNotReplaceComponentLocalVersions(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	devopsDir := filepath.Join(projectRoot, "erun-devops")
	for _, dir := range []string{
		filepath.Join(devopsDir, "docker", "erun-devops"),
		filepath.Join(devopsDir, "docker", "erun-ubuntu"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(devopsDir, "docker", "erun-devops", "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write devops Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(devopsDir, "docker", "erun-ubuntu", "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write ubuntu Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(devopsDir, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(devopsDir, "docker", "erun-ubuntu", "VERSION"), []byte("noble-20260217\n"), 0o644); err != nil {
		t.Fatalf("write ubuntu VERSION: %v", err)
	}

	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: filepath.Join(devopsDir, "docker")}, nil
		},
		BuildDockerImage: func(buildInput common.DockerBuildSpec, stdout, stderr io.Writer) error {
			t.Fatalf("unexpected build execution: %s", buildInput.Image.Tag)
			return nil
		},
	})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"build", "--dry-run", "--version", "1.0.0-pr.abc1234"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stderr.String()
	if !strings.Contains(output, "erun-devops:1.0.0-pr.abc1234") {
		t.Fatalf("expected overridden devops tag in output:\n%s", output)
	}
	if !strings.Contains(output, "erun-ubuntu:noble-20260217") {
		t.Fatalf("expected component-local ubuntu tag in output:\n%s", output)
	}
	if strings.Contains(output, "erun-ubuntu:1.0.0-pr.abc1234") {
		t.Fatalf("did not expect ubuntu tag override:\n%s", output)
	}
}

func TestRootBuildCommandVersionOverrideBuildsWithoutPushing(t *testing.T) {
	projectRoot, _, _, componentDirs, wantTags := setupMultiDockerProject(t, "tenant-a")
	if err := os.Remove(filepath.Join(componentDirs[0], "VERSION")); err != nil {
		t.Fatalf("remove runtime VERSION: %v", err)
	}

	var built []dockerBuildCall
	cmd := newTestRootCmd(testRootDeps{
		OptionalBuildFindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: projectRoot}, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			built = append(built, req)
			return nil
		}),
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			t.Fatalf("unexpected push request: %+v", req)
			return nil
		}),
	})
	cmd.SetArgs([]string{"build", "--version", "1.0.7"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	gotTags := make([]string, 0, len(built))
	for _, req := range built {
		gotTags = append(gotTags, req.Tag)
	}
	assertTagSet(t, gotTags, []string{
		"erunpaas/tenant-a-devops:1.0.7",
		"erunpaas/erun-dind:28.1.1",
		"erunpaas/erun-ubuntu:noble-20260217",
	})
	if len(gotTags) != len(wantTags) {
		t.Fatalf("unexpected build count: got %d want %d", len(gotTags), len(wantTags))
	}
}

func TestRootBuildCommandDeployBuildsPushesAndDeploysOverriddenVersion(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	chartPath := createHelmChartFixture(t, projectRoot, "erun-devops")
	buildDir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := common.SaveERunConfig(common.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: common.DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveEnvConfig("tenant-a", common.EnvConfig{
		Name:              common.DefaultEnvironment,
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
	}); err != nil {
		t.Fatalf("save env config: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	var builds []dockerBuildCall
	var pushes []dockerPushCall
	var deploys []common.HelmDeployParams
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		OptionalBuildFindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            buildDir,
				DockerfilePath: filepath.Join(buildDir, "Dockerfile"),
			}, nil
		},
		ResolveKubernetesDeployContext: func() (common.KubernetesDeployContext, error) {
			return common.KubernetesDeployContext{
				Dir:           buildDir,
				ComponentName: "erun-devops",
				ChartPath:     chartPath,
			}, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			builds = append(builds, req)
			return nil
		}),
		PushDockerImage: pushCallFunc(func(req dockerPushCall) error {
			pushes = append(pushes, req)
			return nil
		}),
		DeployHelmChart: func(req common.HelmDeployParams) error {
			deploys = append(deploys, req)
			return nil
		},
	})
	cmd.SetArgs([]string{"build", "--deploy", "--version", "1.0.7"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if len(builds) != 1 {
		t.Fatalf("unexpected build requests: %+v", builds)
	}
	if len(pushes) != 1 {
		t.Fatalf("unexpected push requests: %+v", pushes)
	}
	if len(deploys) != 1 {
		t.Fatalf("unexpected deploy requests: %+v", deploys)
	}
	if builds[0].Tag != "erunpaas/erun-devops:1.0.7" {
		t.Fatalf("unexpected build request: %+v", builds[0])
	}
	if pushes[0].Tag != builds[0].Tag {
		t.Fatalf("expected push to use built tag, got builds=%+v pushes=%+v", builds, pushes)
	}
	if deploys[0].Version != "1.0.7" {
		t.Fatalf("unexpected deploy request: %+v", deploys[0])
	}
}

func TestBuildCommandHiddenEnvironmentOverrideUsesProvidedEnvironment(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	projectRoot := t.TempDir()
	buildDir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := common.SaveTenantConfig(common.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "local",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := common.SaveProjectConfig(projectRoot, common.ProjectConfig{
		Environments: map[string]common.ProjectEnvironmentConfig{
			"local": {ContainerRegistry: "local-registry"},
			"prod":  {ContainerRegistry: "prod-registry"},
		},
	}); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	var built dockerBuildCall
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{
				Dir:            buildDir,
				DockerfilePath: filepath.Join(buildDir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			built = req
			return nil
		}),
	})
	cmd.SetArgs([]string{"build", "--environment", "prod", "--project-root", projectRoot})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if built.Tag != "prod-registry/erun-devops:1.0.0" {
		t.Fatalf("unexpected build request: %+v", built)
	}
}

func TestBuildCommandRejectsReleaseWithVersion(t *testing.T) {
	cmd := newTestRootCmd(testRootDeps{})
	cmd.SetArgs([]string{"devops", "container", "build", "--release", "--version", "1.0.7"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if err.Error() != "release build cannot be combined with explicit version override" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildCommandDryRunReleaseShowsReleaseAndBuildVersionTrace(t *testing.T) {
	projectRoot := createReleaseGitRepo(t, "develop")
	if err := os.WriteFile(filepath.Join(projectRoot, "build.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write build.sh: %v", err)
	}

	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		OptionalBuildFindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: projectRoot}, nil
		},
	})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"build", "--dry-run", "--release"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stderr.String()
	if !strings.Contains(output, "release: branch=develop mode=candidate version=1.4.2-rc.") {
		t.Fatalf("expected release trace, got:\n%s", output)
	}
	if !strings.Contains(output, "ERUN_BUILD_VERSION=1.4.2-rc.") || !strings.Contains(output, "./build.sh") {
		t.Fatalf("expected build trace with release version env, got:\n%s", output)
	}
	if !strings.Contains(output, "release version: 1.4.2-rc.") {
		t.Fatalf("expected final release version output, got:\n%s", output)
	}
}

func TestBuildCommandDryRunReleaseShowsPushCommandsForReleaseTaggedDockerBuilds(t *testing.T) {
	projectRoot := createReleaseGitRepo(t, "main")
	if err := common.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}

	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		OptionalBuildFindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: projectRoot}, nil
		},
	})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"build", "--dry-run", "--release"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stderr.String()
	if !strings.Contains(output, "docker buildx inspect erun-multiarch") {
		t.Fatalf("expected buildx inspect trace, got:\n%s", output)
	}
	if !strings.Contains(output, "docker buildx create --name erun-multiarch --driver docker-container") {
		t.Fatalf("expected buildx create trace, got:\n%s", output)
	}
	if !strings.Contains(output, "docker buildx inspect --builder erun-multiarch --bootstrap") {
		t.Fatalf("expected buildx bootstrap trace, got:\n%s", output)
	}
	if !strings.Contains(output, "docker buildx build --builder erun-multiarch --platform 'linux/amd64,linux/arm64' -t erunpaas/api:1.4.2") {
		t.Fatalf("expected release build trace, got:\n%s", output)
	}
	if !strings.Contains(output, "docker build -t erunpaas/base:9.9.9") {
		t.Fatalf("expected component-local build trace, got:\n%s", output)
	}
	if !strings.Contains(output, "--push") {
		t.Fatalf("expected multi-platform release push trace, got:\n%s", output)
	}
	if strings.Contains(output, "docker push erunpaas/api:1.4.2") || strings.Contains(output, "docker push erunpaas/base:9.9.9") {
		t.Fatalf("did not expect separate docker push trace, got:\n%s", output)
	}
}

func TestBuildCommandDryRunReleaseForceIncludesTagDeletionForStaleReleaseTag(t *testing.T) {
	projectRoot := createReleaseGitRepo(t, "main")
	remoteRoot := filepath.Join(t.TempDir(), "origin.git")
	runGitCommand(t, t.TempDir(), "init", "--bare", remoteRoot)
	runGitCommand(t, projectRoot, "remote", "add", "origin", remoteRoot)
	runGitCommand(t, projectRoot, "push", "-u", "origin", "main")
	runGitCommand(t, projectRoot, "tag", "-a", "v1.4.2", "-m", "Release 1.4.2")
	runGitCommand(t, projectRoot, "push", "origin", "v1.4.2")
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "README.tmp"), []byte("change\n"), 0o644); err != nil {
		t.Fatalf("write temp change: %v", err)
	}
	runGitCommand(t, projectRoot, "add", "erun-devops/README.tmp")
	runGitCommand(t, projectRoot, "commit", "-m", "advance head")
	runGitCommand(t, projectRoot, "push", "origin", "main")

	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		OptionalBuildFindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: projectRoot}, nil
		},
	})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"build", "--dry-run", "--release", "--force"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stderr.String()
	for _, want := range []string{
		"git tag -d v1.4.2",
		"git push --delete origin v1.4.2",
		"docker buildx build --builder erun-multiarch --platform 'linux/amd64,linux/arm64' -t erunpaas/api:1.4.2",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected dry-run output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestBuildCommandDryRunBuildsLinuxPackagesFromProjectRoot(t *testing.T) {
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

	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		OptionalBuildFindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: projectRoot}, nil
		},
	})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"build", "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stderr.String()
	if common.LinuxPackageBuildsSupported() {
		if !strings.Contains(output, "./build.sh") {
			t.Fatalf("expected linux build trace, got:\n%s", output)
		}
	} else if !strings.Contains(output, "skipping linux package scripts: host is not Linux or dpkg-deb is unavailable") {
		t.Fatalf("expected linux build skip trace, got:\n%s", output)
	}
}

func TestBuildCommandDryRunReleasePublishesLinuxPackagesWithoutDockerBuilds(t *testing.T) {
	projectRoot := t.TempDir()
	releaseRoot := filepath.Join(projectRoot, "erun-devops")
	linuxComponentDir := filepath.Join(releaseRoot, "linux", "erun-cli")
	if err := os.MkdirAll(linuxComponentDir, 0o755); err != nil {
		t.Fatalf("mkdir linux component dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(releaseRoot, "VERSION"), []byte("1.4.2\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(linuxComponentDir, "release.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write release.sh: %v", err)
	}
	runGitCommand(t, projectRoot, "init", "-b", "develop")
	runGitCommand(t, projectRoot, "config", "user.email", "codex@example.com")
	runGitCommand(t, projectRoot, "config", "user.name", "Codex")
	runGitCommand(t, projectRoot, "add", ".")
	runGitCommand(t, projectRoot, "commit", "-m", "initial")
	if err := common.SaveProjectConfig(projectRoot, common.ProjectConfig{}); err != nil {
		t.Fatalf("SaveProjectConfig failed: %v", err)
	}

	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		OptionalBuildFindProjectRoot: func() (string, string, error) {
			return "tenant-a", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: projectRoot}, nil
		},
	})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"build", "--dry-run", "--release"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	output := stderr.String()
	if !strings.Contains(output, "release: branch=develop mode=candidate version=1.4.2-rc.") {
		t.Fatalf("expected release trace, got:\n%s", output)
	}
	if common.LinuxPackageBuildsSupported() {
		if !strings.Contains(output, "ERUN_BUILD_VERSION=1.4.2-rc.") || !strings.Contains(output, "./release.sh") {
			t.Fatalf("expected linux release trace, got:\n%s", output)
		}
	} else if !strings.Contains(output, "skipping linux package scripts: host is not Linux or dpkg-deb is unavailable") {
		t.Fatalf("expected linux release skip trace, got:\n%s", output)
	}
}

func projectConfigWithSingleRegistry(registry string) common.ProjectConfig {
	return common.ProjectConfig{
		Environments: map[string]common.ProjectEnvironmentConfig{
			common.DefaultEnvironment: {ContainerRegistry: registry},
		},
	}
}

func hasSubcommand(cmd *cobra.Command, name string) bool {
	for _, subcommand := range cmd.Commands() {
		if subcommand.Name() == name {
			return true
		}
	}
	return false
}
