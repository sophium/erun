package eruncommon

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type OpenRuntimeStore interface {
	OpenStore
	DockerStore
}

type DockerContainerRunnerFunc func([]string, io.Reader, io.Writer, io.Writer) error

type OpenRuntimeSpec struct {
	Target       OpenResult
	Build        *DockerBuildSpec
	Image        DockerImageReference
	WorktreePath string
	DockerArgs   []string
}

type dockerContainerMount struct {
	Source   string
	Target   string
	ReadOnly bool
}

func ResolveOpenRuntimeSpec(store OpenRuntimeStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, target OpenResult) (OpenRuntimeSpec, bool, error) {
	if !isLocalEnvironment(target.Environment) {
		return OpenRuntimeSpec{}, false, nil
	}

	spec, ok, err := resolveOpenRuntimeSpec(store, findProjectRoot, resolveBuildContext, now, target)
	if err != nil {
		return OpenRuntimeSpec{}, false, err
	}
	return spec, ok, nil
}

func resolveOpenRuntimeSpec(store OpenRuntimeStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, target OpenResult) (OpenRuntimeSpec, bool, error) {
	if store == nil {
		store = ConfigStore{}
	}
	if findProjectRoot == nil {
		findProjectRoot = FindProjectRoot
	}
	if resolveBuildContext == nil {
		resolveBuildContext = ResolveDockerBuildContext
	}
	if now == nil {
		now = time.Now
	}

	for _, componentName := range openRuntimeComponentNames(target.Tenant) {
		spec, ok, err := resolveOpenRuntimeSpecForComponent(store, findProjectRoot, resolveBuildContext, now, target, componentName)
		if err != nil {
			return OpenRuntimeSpec{}, false, err
		}
		if ok {
			return spec, true, nil
		}
	}

	return OpenRuntimeSpec{}, false, nil
}

func resolveOpenRuntimeSpecForComponent(store OpenRuntimeStore, findProjectRoot ProjectFinderFunc, resolveBuildContext BuildContextResolverFunc, now NowFunc, target OpenResult, componentName string) (OpenRuntimeSpec, bool, error) {
	buildContext, ok, err := FindComponentDockerBuildContext(target.RepoPath, componentName)
	if err != nil || !ok {
		return OpenRuntimeSpec{}, ok, err
	}

	imageRef, err := ResolveDockerImageReference(store, findProjectRoot, resolveBuildContext, now, buildContext.Dir, DockerCommandTarget{
		ProjectRoot: target.RepoPath,
		Environment: target.Environment,
	})
	if err != nil {
		return OpenRuntimeSpec{}, false, err
	}

	var build *DockerBuildSpec
	if imageRef.IsLocalBuild {
		build, err = ResolveDockerBuildForComponent(store, findProjectRoot, resolveBuildContext, now, target.RepoPath, target.Environment, componentName)
		if err != nil {
			return OpenRuntimeSpec{}, false, err
		}
		if build == nil {
			return OpenRuntimeSpec{}, false, fmt.Errorf("docker build context not found for runtime component %q", componentName)
		}
		imageRef = build.Image
	}

	worktreePath := remoteWorktreePath(ShellLaunchParamsFromResult(target))
	return OpenRuntimeSpec{
		Target:       target,
		Build:        build,
		Image:        imageRef,
		WorktreePath: worktreePath,
		DockerArgs:   openRuntimeDockerArgs(target, worktreePath, imageRef.Tag),
	}, true, nil
}

func (s OpenRuntimeSpec) DockerBuildCommand() []string {
	if s.Build == nil {
		return nil
	}
	cmd := s.Build.command()
	return append([]string{cmd.Name}, cmd.Args...)
}

func (s OpenRuntimeSpec) DockerBuildWorkingDirectory() string {
	if s.Build == nil {
		return ""
	}
	return s.Build.ContextDir
}

func (s OpenRuntimeSpec) DockerCommand() []string {
	return append([]string{"docker"}, s.DockerArgs...)
}

func RunOpenRuntime(ctx Context, spec OpenRuntimeSpec, build DockerImageBuilderFunc, run DockerContainerRunnerFunc) error {
	if spec.Build != nil {
		if err := RunDockerBuild(ctx, *spec.Build, build); err != nil {
			return err
		}
	}

	ctx.TraceCommand("", "docker", spec.DockerArgs...)
	if ctx.DryRun {
		return nil
	}

	if run == nil {
		run = DockerContainerRunner
	}
	return run(spec.DockerArgs, ctx.Stdin, ctx.Stdout, ctx.Stderr)
}

func DockerContainerRunner(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.Command("docker", args...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func openRuntimeDockerArgs(target OpenResult, worktreePath, imageTag string) []string {
	args := []string{"run", "--rm", "-it"}

	for _, entry := range []struct {
		Name  string
		Value string
	}{
		{Name: "ERUN_REPO_PATH", Value: worktreePath},
		{Name: "ERUN_KUBERNETES_CONTEXT", Value: strings.TrimSpace(target.EnvConfig.KubernetesContext)},
		{Name: "ERUN_SHELL_HOST", Value: target.Title},
	} {
		if strings.TrimSpace(entry.Value) == "" {
			continue
		}
		args = append(args, "-e", entry.Name+"="+entry.Value)
	}

	for _, mount := range openRuntimeDockerMounts(target.RepoPath, worktreePath) {
		args = append(args, "-v", dockerContainerMountArg(mount))
	}

	args = append(args, "-w", worktreePath, imageTag, "shell")
	return args
}

func openRuntimeDockerMounts(repoPath, worktreePath string) []dockerContainerMount {
	mounts := []dockerContainerMount{{
		Source: filepath.Clean(repoPath),
		Target: worktreePath,
	}}

	homeDir, err := os.UserHomeDir()
	if err == nil {
		for _, mount := range []dockerContainerMount{
			{Source: filepath.Join(homeDir, ".kube"), Target: "/home/erun/.kube", ReadOnly: true},
			{Source: filepath.Join(homeDir, ".ssh"), Target: "/home/erun/.ssh", ReadOnly: true},
			{Source: filepath.Join(homeDir, ".gitconfig"), Target: "/home/erun/.gitconfig", ReadOnly: true},
			{Source: filepath.Join(homeDir, ".docker"), Target: "/home/erun/.docker", ReadOnly: true},
		} {
			if openRuntimeHostPathExists(mount.Source) {
				mounts = append(mounts, mount)
			}
		}
	}

	if openRuntimeHostPathExists("/var/run/docker.sock") {
		mounts = append(mounts, dockerContainerMount{
			Source: "/var/run/docker.sock",
			Target: "/var/run/docker.sock",
		})
	}

	return mounts
}

func openRuntimeHostPathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func dockerContainerMountArg(mount dockerContainerMount) string {
	value := filepath.Clean(mount.Source) + ":" + mount.Target
	if mount.ReadOnly {
		value += ":ro"
	}
	return value
}
