package eruncommon

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
)

type HostOS string

const (
	HostOSDarwin  HostOS = "darwin"
	HostOSLinux   HostOS = "linux"
	HostOSWindows HostOS = "windows"
	HostOSUnknown HostOS = "unknown"
)

type ContainerRuntime string

const (
	ContainerRuntimeDocker         ContainerRuntime = "docker"
	ContainerRuntimeContainerd     ContainerRuntime = "containerd"
	ContainerRuntimePodman         ContainerRuntime = "podman"
	ContainerRuntimeRancherDesktop ContainerRuntime = "rancher-desktop"
	ContainerRuntimeUnknown        ContainerRuntime = "unknown"
)

type KubernetesInstallationType string

const (
	KubernetesInstallationNone KubernetesInstallationType = "none"
	KubernetesInstallationK3s  KubernetesInstallationType = "k3s"
)

const defaultK3sKubeconfigPath = "/etc/rancher/k3s/k3s.yaml"

type HostInfo struct {
	OS      HostOS
	Arch    string
	HomeDir string
}

type KubernetesInstallation struct {
	Type           KubernetesInstallationType
	BinaryPath     string
	KubeconfigPath string
}

type HostRuntime struct {
	Host                   HostInfo
	ContainerRuntime       ContainerRuntime
	ContainerSocketPath    string
	KubernetesInstallation KubernetesInstallation
}

var (
	currentGOOS     = func() string { return runtime.GOOS }
	currentGOARCH   = func() string { return runtime.GOARCH }
	hostUserHomeDir = os.UserHomeDir
	hostPathExists  = func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	}
	hostLookPath = exec.LookPath
)

func DetectHost() HostInfo {
	homeDir, _ := hostUserHomeDir()
	return HostInfo{
		OS:      classifyHostOS(currentGOOS()),
		Arch:    currentGOARCH(),
		HomeDir: homeDir,
	}
}

func DetectHostRuntime() HostRuntime {
	host := DetectHost()
	containerRuntime, socketPath, ok := DetectContainerRuntime(host)
	if !ok {
		containerRuntime = ContainerRuntimeUnknown
		socketPath = ""
	}
	return HostRuntime{
		Host:                   host,
		ContainerRuntime:       containerRuntime,
		ContainerSocketPath:    socketPath,
		KubernetesInstallation: DetectKubernetesInstallation(host),
	}
}

func DetectContainerRuntime(host HostInfo) (ContainerRuntime, string, bool) {
	for _, candidate := range containerRuntimeSocketCandidates(host) {
		if hostPathExists(candidate.path) {
			return candidate.runtime, candidate.path, true
		}
	}
	return ContainerRuntimeUnknown, "", false
}

func DetectKubernetesInstallation(host HostInfo) KubernetesInstallation {
	if host.OS != HostOSLinux {
		return KubernetesInstallation{Type: KubernetesInstallationNone}
	}

	installation := KubernetesInstallation{Type: KubernetesInstallationNone}
	if binaryPath, err := hostLookPath("k3s"); err == nil {
		installation.Type = KubernetesInstallationK3s
		installation.BinaryPath = binaryPath
	}
	if hostPathExists(defaultK3sKubeconfigPath) {
		installation.Type = KubernetesInstallationK3s
		installation.KubeconfigPath = defaultK3sKubeconfigPath
	}
	return installation
}

func (h HostInfo) DockerMountPath(path string) string {
	if path == "" {
		return ""
	}
	if h.OS != HostOSWindows {
		return filepath.Clean(path)
	}

	normalized := strings.ReplaceAll(path, "\\", "/")
	if len(normalized) >= 2 && normalized[1] == ':' {
		drive := unicode.ToLower(rune(normalized[0]))
		remainder := strings.TrimPrefix(normalized[2:], "/")
		if remainder != "" {
			remainder = "/" + remainder
		}
		return "/" + string(drive) + remainder
	}
	return normalized
}

func (h HostInfo) DockerVolumeSource(path string) string {
	if path == "" {
		return ""
	}
	if h.OS == HostOSWindows && strings.HasPrefix(path, `\\.\`) {
		return strings.ReplaceAll(path, "\\", "/")
	}
	return h.DockerMountPath(path)
}

func (h HostInfo) JoinPath(elem ...string) string {
	if h.OS != HostOSWindows {
		return filepath.Join(elem...)
	}

	parts := make([]string, 0, len(elem))
	for index, part := range elem {
		if part == "" {
			continue
		}
		if index == 0 {
			part = strings.TrimRight(part, `\/`)
		} else {
			part = strings.Trim(part, `\/`)
		}
		if part == "" {
			continue
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return ""
	}

	path := parts[0]
	for _, part := range parts[1:] {
		path += `\` + part
	}
	return path
}

type containerRuntimeSocketCandidate struct {
	runtime ContainerRuntime
	path    string
}

func containerRuntimeSocketCandidates(host HostInfo) []containerRuntimeSocketCandidate {
	homeDir := strings.TrimSpace(host.HomeDir)

	switch host.OS {
	case HostOSWindows:
		return []containerRuntimeSocketCandidate{
			{runtime: ContainerRuntimeDocker, path: `\\.\pipe\docker_engine`},
			{runtime: ContainerRuntimeRancherDesktop, path: `\\.\pipe\docker_engine`},
			{runtime: ContainerRuntimeContainerd, path: `\\.\pipe\containerd-containerd`},
		}
	case HostOSDarwin:
		return []containerRuntimeSocketCandidate{
			{runtime: ContainerRuntimeDocker, path: "/var/run/docker.sock"},
			{runtime: ContainerRuntimeRancherDesktop, path: host.JoinPath(homeDir, ".rd", "docker.sock")},
			{runtime: ContainerRuntimeContainerd, path: host.JoinPath(homeDir, ".rd", "containerd", "containerd.sock")},
			{runtime: ContainerRuntimePodman, path: host.JoinPath(homeDir, ".local", "share", "containers", "podman", "machine", "podman.sock")},
		}
	default:
		return []containerRuntimeSocketCandidate{
			{runtime: ContainerRuntimeDocker, path: "/var/run/docker.sock"},
			{runtime: ContainerRuntimeRancherDesktop, path: host.JoinPath(homeDir, ".rd", "docker.sock")},
			{runtime: ContainerRuntimeContainerd, path: "/run/containerd/containerd.sock"},
			{runtime: ContainerRuntimePodman, path: "/run/podman/podman.sock"},
		}
	}
}

func classifyHostOS(goos string) HostOS {
	switch goos {
	case "darwin":
		return HostOSDarwin
	case "linux":
		return HostOSLinux
	case "windows":
		return HostOSWindows
	default:
		return HostOSUnknown
	}
}
