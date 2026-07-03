package eruncommon

import (
	"os"
	"os/exec"
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
	currentGOARCH   = func() string { return runtime.GOARCH }
	hostUserHomeDir = os.UserHomeDir
	hostLookPath    = exec.LookPath
	hostOSOverEnv   = func() string { return os.Getenv("ERUN_HOST_OS_OVERRIDE") }
)

func DetectHost() HostInfo {
	homeDir, _ := hostUserHomeDir()
	return HostInfo{
		OS:      resolveDetectedHostOS(),
		Arch:    currentGOARCH(),
		HomeDir: homeDir,
	}
}

// resolveDetectedHostOS honors ERUN_HOST_OS_OVERRIDE as a test seam so the
// integration suite can pin platform-dependent goldens on any host; production
// must never set it.
func resolveDetectedHostOS() HostOS {
	if override := strings.ToLower(strings.TrimSpace(hostOSOverEnv())); override != "" {
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
