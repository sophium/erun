package internal

import (
	"os"
	"runtime"
	"strings"
	"unicode"
)

// HostOS represents the host operating system we are running on.
type HostOS string

// Supported host operating systems.
const (
	HostOSDarwin  HostOS = "darwin"
	HostOSLinux   HostOS = "linux"
	HostOSWindows HostOS = "windows"
	HostOSUnknown HostOS = "unknown"
)

// OSInfo captures information about the host operating system.
type OSInfo struct {
	Type    HostOS
	Arch    string
	HomeDir string
}

var userHomeDir = osUserHomeDir

// DetectOS detects the current host operating system and returns its metadata.
func DetectOS() OSInfo {
	home, _ := userHomeDir()
	return OSInfo{
		Type:    classifyHostOS(runtime.GOOS),
		Arch:    runtime.GOARCH,
		HomeDir: home,
	}
}

// DockerMountPath converts a local path so it can safely be used as part of a
// docker volume specification. This is mainly needed on Windows where docker
// expects /drive/path formatting.
func (info OSInfo) DockerMountPath(path string) string {
	if path == "" {
		return ""
	}
	if info.Type != HostOSWindows {
		return path
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

// DockerVolumeSource returns the host path formatted for docker volume mounts.
// It understands both filesystem paths and Windows named pipes.
func (info OSInfo) DockerVolumeSource(path string) string {
	if path == "" {
		return ""
	}
	if info.Type == HostOSWindows && strings.HasPrefix(path, `\\.\`) {
		return strings.ReplaceAll(path, "\\", "/")
	}
	return info.DockerMountPath(path)
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

// osUserHomeDir is extracted to allow overriding in tests.
var osUserHomeDir = func() (string, error) {
	return os.UserHomeDir()
}
