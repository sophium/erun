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
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func buildCallFunc(run func(dockerBuildCall) error) common.DockerImageBuilderFunc {
	return func(dir, dockerfilePath, tag string, stdout, stderr io.Writer) error {
		return run(dockerBuildCall{
			Dir:            dir,
			DockerfilePath: dockerfilePath,
			Tag:            tag,
			Stdout:         stdout,
			Stderr:         stderr,
		})
	}
}

func buildScriptCallFunc(run func(buildScriptCall) error) common.BuildScriptRunnerFunc {
	return func(dir, path string, stdin io.Reader, stdout, stderr io.Writer) error {
		return run(buildScriptCall{
			Dir:    dir,
			Path:   path,
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

func TestRootBuildShorthandRunsProjectBuildScriptWhenPresent(t *testing.T) {
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

	var received buildScriptCall
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
			received = req
			return nil
		}),
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			t.Fatalf("unexpected docker build request: %+v", req)
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

	if received.Dir != scriptDir || received.Path != "./build.sh" {
		t.Fatalf("unexpected build script call: %+v", received)
	}
	if received.Stdout != stdout || received.Stderr != stderr {
		t.Fatalf("unexpected output writers: %+v", received)
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

	var received []dockerBuildCall
	cmd := newTestRootCmd(testRootDeps{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (common.DockerBuildContext, error) {
			return common.DockerBuildContext{Dir: dockerDir}, nil
		},
		BuildDockerImage: buildCallFunc(func(req dockerBuildCall) error {
			received = append(received, req)
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

	if len(received) != 3 {
		t.Fatalf("expected 3 build requests, got %d", len(received))
	}

	gotTags := []string{received[0].Tag, received[1].Tag, received[2].Tag}
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
	for _, req := range received {
		if req.Dir != projectRoot {
			t.Fatalf("expected project root build context, got %+v", req)
		}
		if req.Stdout != stdout || req.Stderr != stderr {
			t.Fatalf("unexpected output writers: %+v", req)
		}
	}
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

func TestRootPushShorthandUsesResolvedImageTag(t *testing.T) {
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
		}, nil, nil, nil),
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
