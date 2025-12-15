package internal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

type ContainerRuntime string

const (
	RuntimeDocker         ContainerRuntime = "docker"
	RuntimeContainerd     ContainerRuntime = "containerd"
	RuntimePodman         ContainerRuntime = "podman"
	RuntimeRancherDesktop ContainerRuntime = "rancher-desktop"
	RuntimeUnknown        ContainerRuntime = "unknown"
)

var (
	ErrDockerBinaryNotFound = errors.New("docker executable not found in PATH")
	ErrDockerSocketNotFound = errors.New("docker socket not detected; ensure the container runtime is running")
)

// DockerEnvironment describes the detected Docker installation on the host.
type DockerEnvironment struct {
	Host       OSInfo
	Runtime    ContainerRuntime
	SocketPath string
	BinaryPath string
}

// DockerRunOptions controls how the docker run command is assembled.
type DockerRunOptions struct {
	Image   string
	Command []string

	Env map[string]string

	WorkspacePath   string
	WorkspaceTarget string
	Workdir         string

	ExtraMounts []DockerMount
	ExtraArgs   []string

	Interactive   bool
	Detach        bool
	KeepContainer bool

	DisableDockerSocket   bool
	DisableWorkspaceMount bool

	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
}

// DockerMount represents a host to container bind mount.
type DockerMount struct {
	Source   string
	Target   string
	ReadOnly bool
}

var (
	lookPath             = exec.LookPath
	newDockerEnvironment = NewDockerEnvironment
	runDockerWithEnv     = func(ctx context.Context, env *DockerEnvironment, opts DockerRunOptions) error {
		return env.Run(ctx, opts)
	}
)

// RunDocker is a convenience function that detects the host Docker environment
// and executes docker run with the provided options. This is the preferred entry
// point for the rest of the application since it automatically handles
// multi-OS quirks such as socket and filesystem mounts.
func RunDocker(ctx context.Context, opts DockerRunOptions) error {
	env, err := newDockerEnvironment()
	if err != nil {
		return err
	}
	return runDockerWithEnv(ctx, env, opts)
}

// NewDockerEnvironment detects the docker binary, socket and runtime.
func NewDockerEnvironment() (*DockerEnvironment, error) {
	host := DetectOS()
	binary, err := findDockerBinary(host)
	if err != nil {
		return nil, err
	}

	runtime, socket, ok := detectRuntime(host)
	if !ok {
		return nil, ErrDockerSocketNotFound
	}

	return &DockerEnvironment{
		Host:       host,
		Runtime:    runtime,
		SocketPath: socket,
		BinaryPath: binary,
	}, nil
}

// DetectContainerRuntime reports the host runtime and if it was detected.
func DetectContainerRuntime() (ContainerRuntime, bool) {
	rt, _, ok := detectRuntime(DetectOS())
	return rt, ok
}

// Run executes docker run with the provided options and the default socket and
// workspace mounts.
func (env *DockerEnvironment) Run(ctx context.Context, opts DockerRunOptions) error {
	args, err := env.buildRunArgs(opts)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, env.BinaryPath, args...)
	cmd.Stdout = pickWriter(opts.Stdout, os.Stdout)
	cmd.Stderr = pickWriter(opts.Stderr, os.Stderr)
	cmd.Stdin = pickReader(opts.Stdin, os.Stdin)

	return cmd.Run()
}

func (env *DockerEnvironment) buildRunArgs(opts DockerRunOptions) ([]string, error) {
	if opts.Image == "" {
		return nil, errors.New("image is required")
	}

	args := []string{"run"}
	if opts.Detach {
		args = append(args, "-d")
	}
	if opts.Interactive {
		args = append(args, "-it")
	}
	if !opts.KeepContainer {
		args = append(args, "--rm")
	}

	workspaceTarget := opts.WorkspaceTarget
	if workspaceTarget == "" {
		workspaceTarget = "/workspace"
	}

	workdir := opts.Workdir

	if !opts.DisableDockerSocket {
		if env.SocketPath == "" {
			return nil, ErrDockerSocketNotFound
		}
		args = appendVolume(args, env.Host.DockerVolumeSource(env.SocketPath), "/var/run/docker.sock", false)
	}

	if !opts.DisableWorkspaceMount {
		workspacePath := opts.WorkspacePath
		if workspacePath == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return nil, fmt.Errorf("determine working directory: %w", err)
			}
			workspacePath = cwd
		}
		args = appendVolume(args, env.Host.DockerVolumeSource(workspacePath), workspaceTarget, false)
		if workdir == "" {
			workdir = workspaceTarget
		}
	}

	for _, mount := range opts.ExtraMounts {
		if mount.Source == "" || mount.Target == "" {
			return nil, errors.New("mounts require both source and target paths")
		}
		args = appendVolume(args, env.Host.DockerVolumeSource(mount.Source), mount.Target, mount.ReadOnly)
	}

	if workdir != "" {
		args = append(args, "--workdir", workdir)
	}

	if len(opts.Env) > 0 {
		keys := make([]string, 0, len(opts.Env))
		for k := range opts.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			args = append(args, "-e", fmt.Sprintf("%s=%s", k, opts.Env[k]))
		}
	}

	if len(opts.ExtraArgs) > 0 {
		args = append(args, opts.ExtraArgs...)
	}

	args = append(args, opts.Image)
	args = append(args, opts.Command...)

	return args, nil
}

func appendVolume(args []string, source, target string, readOnly bool) []string {
	mount := fmt.Sprintf("%s:%s", source, target)
	if readOnly {
		mount += ":ro"
	}
	return append(args, "-v", mount)
}

func pickWriter(w io.Writer, fallback io.Writer) io.Writer {
	if w != nil {
		return w
	}
	return fallback
}

func pickReader(r io.Reader, fallback io.Reader) io.Reader {
	if r != nil {
		return r
	}
	return fallback
}

func findDockerBinary(info OSInfo) (string, error) {
	candidates := []string{"docker"}
	if info.Type == HostOSWindows {
		candidates = append([]string{"docker.exe"}, candidates...)
	}

	for _, name := range candidates {
		if path, err := lookPath(name); err == nil {
			return path, nil
		}
	}
	return "", ErrDockerBinaryNotFound
}

func detectRuntime(info OSInfo) (ContainerRuntime, string, bool) {
	for _, s := range candidateSockets(info) {
		if socketExists(s.path) {
			return s.runtime, s.path, true
		}
	}
	return RuntimeUnknown, "", false
}

type socketCandidate struct {
	runtime ContainerRuntime
	path    string
}

func candidateSockets(info OSInfo) []socketCandidate {
	home := info.HomeDir
	switch info.Type {
	case HostOSWindows:
		return []socketCandidate{
			{RuntimeDocker, `\\.\\pipe\\docker_engine`},
			{RuntimeRancherDesktop, `\\.\\pipe\\docker_engine`},
			{RuntimeContainerd, `\\.\\pipe\\containerd-containerd`},
		}
	case HostOSDarwin:
		return []socketCandidate{
			{RuntimeDocker, "/var/run/docker.sock"},
			{RuntimeRancherDesktop, filepath.Join(home, ".rd", "docker.sock")},
			{RuntimeContainerd, filepath.Join(home, ".rd", "containerd", "containerd.sock")},
			{RuntimePodman, filepath.Join(home, ".local", "share", "containers", "podman", "machine", "podman.sock")},
		}
	default:
		return []socketCandidate{
			{RuntimeDocker, "/var/run/docker.sock"},
			{RuntimeRancherDesktop, filepath.Join(home, ".rd", "docker.sock")},
			{RuntimeContainerd, "/run/containerd/containerd.sock"},
			{RuntimePodman, "/run/podman/podman.sock"},
		}
	}
}

func socketExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
