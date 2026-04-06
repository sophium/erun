package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/internal"
	"github.com/sophium/erun/internal/bootstrap"
	"github.com/sophium/erun/internal/opener"
	"github.com/spf13/cobra"
)

func TestNewRootCmdRegistersDevopsContainerBuildCommand(t *testing.T) {
	cmd := NewRootCmd(Dependencies{
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{Dir: t.TempDir()}, nil
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
	cmd := NewRootCmd(Dependencies{
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{Dir: t.TempDir()}, nil
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
	cmd := NewRootCmd(Dependencies{
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			dir := t.TempDir()
			return DockerBuildContext{
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

	cmd := NewRootCmd(Dependencies{
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{Dir: dockerDir}, nil
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
	cmd := NewRootCmd(Dependencies{
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{Dir: t.TempDir()}, nil
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
	if err := internal.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("noble-20260217\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}
	buildContext := DockerBuildContext{
		Dir:            workdir,
		DockerfilePath: filepath.Join(workdir, "Dockerfile"),
	}

	var received DockerBuildRequest
	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return buildContext, nil
		},
		BuildDockerImage: func(req DockerBuildRequest) error {
			received = req
			return nil
		},
		LaunchShell: func(req opener.ShellLaunchRequest) error {
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
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               "erun",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: bootstrap.DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
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

	var received []DockerBuildRequest
	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{Dir: dockerDir}, nil
		},
		BuildDockerImage: func(req DockerBuildRequest) error {
			received = append(received, req)
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
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               "erun",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: bootstrap.DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	fixedNow := time.Date(2026, time.April, 6, 12, 34, 56, 0, time.UTC)
	var received DockerBuildRequest
	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: func(req DockerBuildRequest) error {
			received = req
			return nil
		},
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
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               "erun",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: bootstrap.DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	stderr := new(bytes.Buffer)
	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: func(req DockerBuildRequest) error {
			t.Fatalf("unexpected build request during dry-run: %+v", req)
			return nil
		},
		Now: func() time.Time {
			return time.Date(2026, time.April, 6, 13, 16, 30, 0, time.UTC)
		},
	})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"build", "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if got := stderr.String(); !strings.Contains(got, "[dry-run] docker build -t erunpaas/erun-devops:1.1.0") {
		t.Fatalf("expected dry-run build trace, got %q", got)
	}
	if got := stderr.String(); strings.Contains(got, "decision:") {
		t.Fatalf("did not expect decision notes without -v during dry-run, got %q", got)
	}
}

func TestRootBuildShorthandVerbosePrintsTraceBeforeExecuting(t *testing.T) {
	projectRoot := t.TempDir()
	workdir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := internal.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	stderr := new(bytes.Buffer)
	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: func(req DockerBuildRequest) error {
			return nil
		},
	})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"-v", "build"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if got := stderr.String(); !strings.Contains(got, "[trace] docker build -t erunpaas/erun-devops:1.1.0") {
		t.Fatalf("expected verbose build trace, got %q", got)
	}
	if got := stderr.String(); strings.Contains(got, "decision:") {
		t.Fatalf("did not expect decision notes at -v, got %q", got)
	}
}

func TestRootBuildShorthandDoubleVerbosePrintsDecisionNotesBeforeExecuting(t *testing.T) {
	projectRoot := t.TempDir()
	workdir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := internal.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	stderr := new(bytes.Buffer)
	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: func(req DockerBuildRequest) error {
			return nil
		},
	})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"-vv", "build"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	got := stderr.String()
	if !strings.Contains(got, "[trace] docker build -t erunpaas/erun-devops:1.1.0") {
		t.Fatalf("expected verbose build trace, got %q", got)
	}
	if !strings.Contains(got, "[trace] decision: resolved registry=erunpaas") {
		t.Fatalf("expected decision notes at -vv, got %q", got)
	}
}

func TestDevopsContainerBuildFailsWithoutDockerfile(t *testing.T) {
	cmd := NewRootCmd(Dependencies{
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{Dir: t.TempDir()}, nil
		},
		BuildDockerImage: func(req DockerBuildRequest) error {
			t.Fatalf("unexpected build request: %+v", req)
			return nil
		},
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
	if err := internal.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	var received DockerPushRequest
	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		PushDockerImage: func(req DockerPushRequest) error {
			received = req
			return nil
		},
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
	if err := internal.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
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
	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		SelectRunner: func(prompt promptui.Select) (int, string, error) {
			return 0, loginAndRetryPushOption, nil
		},
		PushDockerImage: func(req DockerPushRequest) error {
			pushCalls++
			if pushCalls == 1 {
				return dockerRegistryAuthError{
					tag:      req.Tag,
					registry: dockerRegistryFromImageTag(req.Tag),
					message:  "push access denied: insufficient_scope: authorization failed",
					err:      errors.New("exit status 1"),
				}
			}
			return nil
		},
		LoginToDockerRegistry: func(req DockerLoginRequest) error {
			loginRegistry = req.Registry
			return nil
		},
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
	if err := internal.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	var received DockerPushRequest
	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		PushDockerImage: func(req DockerPushRequest) error {
			received = req
			return nil
		},
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
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               "erun",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: bootstrap.DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}

	if err := internal.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	var built DockerBuildRequest
	var received DockerPushRequest
	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: func(req DockerBuildRequest) error {
			built = req
			return nil
		},
		PushDockerImage: func(req DockerPushRequest) error {
			received = req
			return nil
		},
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
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               "erun",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: bootstrap.DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}

	fixedNow := time.Date(2026, time.April, 6, 12, 34, 56, 0, time.UTC)
	var received DockerBuildRequest
	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: func(req DockerBuildRequest) error {
			received = req
			return nil
		},
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
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               "erun",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: bootstrap.DefaultEnvironment,
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	stderr := new(bytes.Buffer)
	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		BuildDockerImage: func(req DockerBuildRequest) error {
			t.Fatalf("unexpected build request during dry-run: %+v", req)
			return nil
		},
		PushDockerImage: func(req DockerPushRequest) error {
			t.Fatalf("unexpected push request during dry-run: %+v", req)
			return nil
		},
	})
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"push", "--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	got := stderr.String()
	if !strings.Contains(got, "[dry-run] docker build -t erunpaas/erun-devops:1.1.0") {
		t.Fatalf("expected dry-run build trace, got %q", got)
	}
	if !strings.Contains(got, "[dry-run] docker push erunpaas/erun-devops:1.1.0") {
		t.Fatalf("expected dry-run push trace, got %q", got)
	}
}

func TestDevopsContainerPushFailsWithoutDockerfile(t *testing.T) {
	cmd := NewRootCmd(Dependencies{
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{Dir: t.TempDir()}, nil
		},
		PushDockerImage: func(req DockerPushRequest) error {
			t.Fatalf("unexpected push request: %+v", req)
			return nil
		},
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
	if err := internal.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("erunpaas")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("1.1.0\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	expectedErr := dockerRegistryAuthError{
		tag:      "erunpaas/erun-devops:1.1.0",
		registry: "",
		message:  "push access denied: insufficient_scope: authorization failed",
		err:      errors.New("exit status 1"),
	}

	cmd := NewRootCmd(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{
				Dir:            workdir,
				DockerfilePath: filepath.Join(workdir, "Dockerfile"),
			}, nil
		},
		SelectRunner: func(prompt promptui.Select) (int, string, error) {
			return 1, cancelPushOption, nil
		},
		PushDockerImage: func(req DockerPushRequest) error {
			return expectedErr
		},
		LoginToDockerRegistry: func(req DockerLoginRequest) error {
			t.Fatalf("unexpected login request: %+v", req)
			return nil
		},
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
	if err := internal.SaveERunConfig(internal.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "build",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveEnvConfig("tenant-a", internal.EnvConfig{Name: "build", RepoPath: projectRoot, KubernetesContext: "cluster-build"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	launched := opener.ShellLaunchRequest{}
	cmd := NewRootCmd(Dependencies{
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{Dir: t.TempDir()}, nil
		},
		BuildDockerImage: func(req DockerBuildRequest) error {
			t.Fatalf("unexpected build request: %+v", req)
			return nil
		},
		LaunchShell: func(req opener.ShellLaunchRequest) error {
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
	if err := internal.SaveERunConfig(internal.ERunConfig{DefaultTenant: "tenant-a"}); err != nil {
		t.Fatalf("save erun config: %v", err)
	}
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               "tenant-a",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "push",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveEnvConfig("tenant-a", internal.EnvConfig{Name: "push", RepoPath: projectRoot, KubernetesContext: "cluster-push"}); err != nil {
		t.Fatalf("save env config: %v", err)
	}

	launched := opener.ShellLaunchRequest{}
	cmd := NewRootCmd(Dependencies{
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{Dir: t.TempDir()}, nil
		},
		PushDockerImage: func(req DockerPushRequest) error {
			t.Fatalf("unexpected push request: %+v", req)
			return nil
		},
		LaunchShell: func(req opener.ShellLaunchRequest) error {
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

func TestDefaultDockerBuildContextResolverDetectsDockerfile(t *testing.T) {
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

	result, err := defaultDockerBuildContextResolver()
	if err != nil {
		t.Fatalf("defaultDockerBuildContextResolver failed: %v", err)
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

func TestDefaultDockerBuildContextResolverIgnoresMissingDockerfile(t *testing.T) {
	workdir := t.TempDir()

	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(previousDir) })
	if err := os.Chdir(workdir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	result, err := defaultDockerBuildContextResolver()
	if err != nil {
		t.Fatalf("defaultDockerBuildContextResolver failed: %v", err)
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

func TestRunContainerBuildCommandPropagatesBuildContextErrors(t *testing.T) {
	expectedErr := errors.New("resolve failed")
	cmd := NewBuildCmd(Dependencies{
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{}, expectedErr
		},
	})
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected %v, got %v", expectedErr, err)
	}
}

func TestRunContainerPushCommandPropagatesBuildContextErrors(t *testing.T) {
	expectedErr := errors.New("resolve failed")
	cmd := NewDevopsCmd(Dependencies{
		ResolveDockerBuildContext: func() (DockerBuildContext, error) {
			return DockerBuildContext{}, expectedErr
		},
	})
	cmd.SetArgs([]string{"container", "push"})

	err := cmd.Execute()
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected %v, got %v", expectedErr, err)
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

func TestResolveDockerBuildContextDirUsesProjectRootForModuleDockerDirs(t *testing.T) {
	projectRoot := t.TempDir()
	buildDir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-devops")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}

	contextDir, err := resolveDockerBuildContextDir(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
	}, buildDir)
	if err != nil {
		t.Fatalf("resolveDockerBuildContextDir failed: %v", err)
	}

	if contextDir != projectRoot {
		t.Fatalf("unexpected context dir: %q", contextDir)
	}
}

func TestResolveDockerBuildTagPrefersCurrentDirectoryVersion(t *testing.T) {
	projectRoot := t.TempDir()
	buildDir := filepath.Join(projectRoot, "erun-devops", "docker", "erun-ubuntu")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatalf("mkdir build dir: %v", err)
	}
	if err := internal.SaveProjectConfig(projectRoot, projectConfigWithSingleRegistry("registry.example/team")); err != nil {
		t.Fatalf("save project config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectRoot, "erun-devops", "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write module VERSION: %v", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "VERSION"), []byte("noble-20260217\n"), 0o644); err != nil {
		t.Fatalf("write local VERSION: %v", err)
	}

	tag, err := resolveDockerBuildTag(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
	}, buildDir)
	if err != nil {
		t.Fatalf("resolveDockerBuildTag failed: %v", err)
	}

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
	if err := internal.SaveTenantConfig(internal.TenantConfig{
		Name:               "erun",
		ProjectRoot:        projectRoot,
		DefaultEnvironment: "prod",
	}); err != nil {
		t.Fatalf("save tenant config: %v", err)
	}
	if err := internal.SaveProjectConfig(projectRoot, internal.ProjectConfig{
		Environments: map[string]internal.ProjectEnvironmentConfig{
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

	tag, err := resolveDockerBuildTag(Dependencies{
		FindProjectRoot: func() (string, string, error) {
			return "erun", projectRoot, nil
		},
	}, buildDir)
	if err != nil {
		t.Fatalf("resolveDockerBuildTag failed: %v", err)
	}

	if tag != "registry.example/team/erun-ubuntu:noble-20260217" {
		t.Fatalf("unexpected tag: %q", tag)
	}
}

func projectConfigWithSingleRegistry(registry string) internal.ProjectConfig {
	return internal.ProjectConfig{
		Environments: map[string]internal.ProjectEnvironmentConfig{
			bootstrap.DefaultEnvironment: {ContainerRegistry: registry},
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
