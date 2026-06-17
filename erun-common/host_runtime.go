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

// resolveDetectedHostOS classifies the host OS for runtime decisions. When
// ERUN_HOST_OS_OVERRIDE is set to a recognized OS (darwin/linux/windows), it
// wins over the actual GOOS. The override is a deliberate test seam — the
// integration suite uses it to pin platform-dependent dry-run goldens (most
// notably the IDE launcher scenarios under erun-integration/testdata/open/)
// so the suite stays green on any developer or CI host. Production callers
// should never set it.
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
