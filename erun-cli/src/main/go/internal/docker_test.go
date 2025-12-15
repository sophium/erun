package internal

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestBuildRunArgsIncludesSocketAndWorkspace(t *testing.T) {
	env := &DockerEnvironment{
		Host:       OSInfo{Type: HostOSLinux},
		Runtime:    RuntimeDocker,
		SocketPath: "/var/run/docker.sock",
		BinaryPath: "/usr/bin/docker",
	}

	opts := DockerRunOptions{
		Image:         "alpine",
		Command:       []string{"echo", "hello"},
		WorkspacePath: "/repo",
		Interactive:   true,
		KeepContainer: false,
		ExtraMounts: []DockerMount{
			{Source: "/config", Target: "/config", ReadOnly: true},
		},
		Env: map[string]string{
			"FOO": "bar",
		},
	}

	args, err := env.buildRunArgs(opts)
	if err != nil {
		t.Fatalf("buildRunArgs returned error: %v", err)
	}

	want := []string{
		"run",
		"-it",
		"--rm",
		"-v", "/var/run/docker.sock:/var/run/docker.sock",
		"-v", "/repo:/workspace",
		"-v", "/config:/config:ro",
		"--workdir", "/workspace",
		"-e", "FOO=bar",
		"alpine",
		"echo",
		"hello",
	}

	if !reflect.DeepEqual(args, want) {
		t.Fatalf("unexpected args:\nwant: %#v\ngot:  %#v", want, args)
	}
}

func TestBuildRunArgsWindowsPaths(t *testing.T) {
	env := &DockerEnvironment{
		Host:       OSInfo{Type: HostOSWindows},
		Runtime:    RuntimeDocker,
		SocketPath: `\\.\pipe\docker_engine`,
		BinaryPath: "docker.exe",
	}

	opts := DockerRunOptions{
		Image:         "alpine",
		Command:       []string{"sh"},
		WorkspacePath: `C:\workspace`,
	}

	args, err := env.buildRunArgs(opts)
	if err != nil {
		t.Fatalf("buildRunArgs returned error: %v", err)
	}

	want := []string{
		"run",
		"--rm",
		"-v", "//./pipe/docker_engine:/var/run/docker.sock",
		"-v", "/c/workspace:/workspace",
		"--workdir", "/workspace",
		"alpine",
		"sh",
	}

	if !reflect.DeepEqual(args, want) {
		t.Fatalf("unexpected args:\nwant: %#v\ngot:  %#v", want, args)
	}
}

func TestBuildRunArgsDisableMounts(t *testing.T) {
	env := &DockerEnvironment{
		Host:       OSInfo{Type: HostOSLinux},
		Runtime:    RuntimeDocker,
		SocketPath: "/var/run/docker.sock",
		BinaryPath: "/usr/bin/docker",
	}

	opts := DockerRunOptions{
		Image:                 "alpine",
		Command:               []string{"sh"},
		DisableDockerSocket:   true,
		DisableWorkspaceMount: true,
		KeepContainer:         true,
	}

	args, err := env.buildRunArgs(opts)
	if err != nil {
		t.Fatalf("buildRunArgs returned error: %v", err)
	}

	want := []string{
		"run",
		"alpine",
		"sh",
	}

	if !reflect.DeepEqual(args, want) {
		t.Fatalf("unexpected args: want %#v got %#v", want, args)
	}
}

func TestBuildRunArgsRequiresImage(t *testing.T) {
	env := &DockerEnvironment{}
	_, err := env.buildRunArgs(DockerRunOptions{})
	if err == nil {
		t.Fatalf("expected error when image missing")
	}
}

func TestFindDockerBinaryPrefersExeOnWindows(t *testing.T) {
	prevLookPath := lookPath
	t.Cleanup(func() { lookPath = prevLookPath })

	calls := []string{}
	lookPath = func(bin string) (string, error) {
		calls = append(calls, bin)
		if bin == "docker.exe" {
			return `C:\docker.exe`, nil
		}
		return "", errors.New("not found")
	}

	path, err := findDockerBinary(OSInfo{Type: HostOSWindows})
	if err != nil {
		t.Fatalf("expected binary, got error: %v", err)
	}
	if path != `C:\docker.exe` {
		t.Fatalf("unexpected path %q", path)
	}
	if len(calls) != 1 || calls[0] != "docker.exe" {
		t.Fatalf("expected docker.exe lookup first, got %v", calls)
	}
}

func TestFindDockerBinaryErrors(t *testing.T) {
	prevLookPath := lookPath
	t.Cleanup(func() { lookPath = prevLookPath })

	lookPath = func(bin string) (string, error) {
		return "", errors.New("not found")
	}

	_, err := findDockerBinary(OSInfo{Type: HostOSLinux})
	if !errors.Is(err, ErrDockerBinaryNotFound) {
		t.Fatalf("expected ErrDockerBinaryNotFound, got %v", err)
	}
}

func TestRunDockerUsesWrapper(t *testing.T) {
	prevNew := newDockerEnvironment
	prevRun := runDockerWithEnv
	t.Cleanup(func() {
		newDockerEnvironment = prevNew
		runDockerWithEnv = prevRun
	})

	called := false
	newDockerEnvironment = func() (*DockerEnvironment, error) {
		return &DockerEnvironment{}, nil
	}
	runDockerWithEnv = func(ctx context.Context, env *DockerEnvironment, opts DockerRunOptions) error {
		called = true
		if opts.Image != "alpine" {
			t.Fatalf("expected image forwarded, got %q", opts.Image)
		}
		return nil
	}

	if err := RunDocker(context.Background(), DockerRunOptions{Image: "alpine"}); err != nil {
		t.Fatalf("RunDocker returned error: %v", err)
	}
	if !called {
		t.Fatalf("expected wrapper to execute runDockerWithEnv")
	}
}

func TestRunDockerPropagatesDetectionError(t *testing.T) {
	prevNew := newDockerEnvironment
	t.Cleanup(func() { newDockerEnvironment = prevNew })

	newDockerEnvironment = func() (*DockerEnvironment, error) {
		return nil, errors.New("boom")
	}

	if err := RunDocker(context.Background(), DockerRunOptions{Image: "alpine"}); err == nil {
		t.Fatalf("expected error to be returned")
	}
}
