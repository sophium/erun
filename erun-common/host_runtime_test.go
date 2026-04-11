package eruncommon

import (
	"errors"
	"testing"
)

func TestHostInfoDockerMountPathWindows(t *testing.T) {
	host := HostInfo{OS: HostOSWindows}
	got := host.DockerMountPath(`C:\Users\john\project`)
	want := "/c/Users/john/project"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestHostInfoDockerVolumeSourceWindowsPipe(t *testing.T) {
	host := HostInfo{OS: HostOSWindows}
	got := host.DockerVolumeSource(`\\.\pipe\docker_engine`)
	want := "//./pipe/docker_engine"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestHostInfoJoinPathWindows(t *testing.T) {
	host := HostInfo{OS: HostOSWindows}
	got := host.JoinPath(`C:\Users\john`, ".kube")
	want := `C:\Users\john\.kube`
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestClassifyHostOS(t *testing.T) {
	if classifyHostOS("darwin") != HostOSDarwin {
		t.Fatalf("expected darwin classification")
	}
	if classifyHostOS("linux") != HostOSLinux {
		t.Fatalf("expected linux classification")
	}
	if classifyHostOS("windows") != HostOSWindows {
		t.Fatalf("expected windows classification")
	}
	if classifyHostOS("plan9") != HostOSUnknown {
		t.Fatalf("expected unknown classification")
	}
}

func TestDetectHostUsesOverrides(t *testing.T) {
	prevGOOS := currentGOOS
	prevGOARCH := currentGOARCH
	prevHomeDir := hostUserHomeDir
	t.Cleanup(func() {
		currentGOOS = prevGOOS
		currentGOARCH = prevGOARCH
		hostUserHomeDir = prevHomeDir
	})

	currentGOOS = func() string { return "windows" }
	currentGOARCH = func() string { return "arm64" }
	hostUserHomeDir = func() (string, error) { return `C:\Users\john`, nil }

	host := DetectHost()
	if host.OS != HostOSWindows || host.Arch != "arm64" || host.HomeDir != `C:\Users\john` {
		t.Fatalf("unexpected host: %+v", host)
	}
}

func TestDetectContainerRuntimePrefersMatchingSocket(t *testing.T) {
	prevPathExists := hostPathExists
	t.Cleanup(func() {
		hostPathExists = prevPathExists
	})

	hostPathExists = func(path string) bool {
		return path == "/run/containerd/containerd.sock"
	}

	runtime, socketPath, ok := DetectContainerRuntime(HostInfo{
		OS:      HostOSLinux,
		HomeDir: "/home/test",
	})
	if !ok {
		t.Fatalf("expected runtime detection to succeed")
	}
	if runtime != ContainerRuntimeContainerd || socketPath != "/run/containerd/containerd.sock" {
		t.Fatalf("unexpected runtime detection: %q %q", runtime, socketPath)
	}
}

func TestDetectKubernetesInstallationFindsK3sBinaryAndConfig(t *testing.T) {
	prevLookPath := hostLookPath
	prevPathExists := hostPathExists
	t.Cleanup(func() {
		hostLookPath = prevLookPath
		hostPathExists = prevPathExists
	})

	hostLookPath = func(file string) (string, error) {
		if file == "k3s" {
			return "/usr/local/bin/k3s", nil
		}
		return "", errors.New("not found")
	}
	hostPathExists = func(path string) bool {
		return path == defaultK3sKubeconfigPath
	}

	installation := DetectKubernetesInstallation(HostInfo{OS: HostOSLinux})
	if installation.Type != KubernetesInstallationK3s {
		t.Fatalf("expected k3s installation, got %+v", installation)
	}
	if installation.BinaryPath != "/usr/local/bin/k3s" {
		t.Fatalf("unexpected k3s binary path: %+v", installation)
	}
	if installation.KubeconfigPath != defaultK3sKubeconfigPath {
		t.Fatalf("unexpected k3s kubeconfig path: %+v", installation)
	}
}

func TestDetectKubernetesInstallationIgnoresK3sOutsideLinux(t *testing.T) {
	prevLookPath := hostLookPath
	prevPathExists := hostPathExists
	t.Cleanup(func() {
		hostLookPath = prevLookPath
		hostPathExists = prevPathExists
	})

	hostLookPath = func(file string) (string, error) {
		return "/usr/local/bin/k3s", nil
	}
	hostPathExists = func(path string) bool {
		return true
	}

	installation := DetectKubernetesInstallation(HostInfo{OS: HostOSDarwin})
	if installation.Type != KubernetesInstallationNone {
		t.Fatalf("expected no k3s detection on darwin, got %+v", installation)
	}
}
