package core

import (
	"os"
	"runtime"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
)

type HostInfo struct {
	OS      model.HostOS
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
func ResolveDetectedHostOS() model.HostOS {
	if override := strings.ToLower(strings.TrimSpace(HostOSOverEnv())); override != "" {
		if classified := classifyHostOS(override); classified != model.HostOSUnknown {
			return classified
		}
	}
	return classifyHostOS(currentGOOS())
}

func classifyHostOS(goos string) model.HostOS {
	switch goos {
	case "darwin":
		return model.HostOSDarwin
	case "linux":
		return model.HostOSLinux
	case "windows":
		return model.HostOSWindows
	default:
		return model.HostOSUnknown
	}
}
