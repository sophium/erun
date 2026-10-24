package core

import (
	"os"
	"runtime"
	"strings"
)

type HostOS string

const (
	HostOSDarwin  HostOS = "darwin"
	HostOSLinux   HostOS = "linux"
	HostOSWindows HostOS = "windows"
	HostOSUnknown HostOS = "unknown"
)

type HostInfo struct {
	OS      HostOS
	Arch    string
	HomeDir string
}

var (
	currentGOOS     = func() string { return runtime.GOOS }
	CurrentGOARCH   = func() string { return runtime.GOARCH }
	hostUserHomeDir = os.UserHomeDir
	HostOSOverEnv   = func() string { return os.Getenv("ERUN_HOST_OS_OVERRIDE") }
)

func DetectHost() HostInfo {
	homeDir, _ := hostUserHomeDir()
	return HostInfo{
		OS:      ResolveDetectedHostOS(),
		Arch:    CurrentGOARCH(),
		HomeDir: homeDir,
	}
}

// ResolveDetectedHostOS honors ERUN_HOST_OS_OVERRIDE as a test seam; production never sets it.
func ResolveDetectedHostOS() HostOS {
	if override := strings.ToLower(strings.TrimSpace(HostOSOverEnv())); override != "" {
		if classified := classifyHostOS(override); classified != HostOSUnknown {
			return classified
		}
	}
	return classifyHostOS(currentGOOS())
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
