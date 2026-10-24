package linuxpkg

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
)

// AppendCgroupParentArg nests a build's RUN containers under the environment's CPU cap;
// without it a heavy test stage escapes the sidecar's limit.
func AppendCgroupParentArg(args []string, cgroupParent string) []string {
	if cgroupParent == "" {
		return args
	}
	return append(args, "--cgroup-parent", cgroupParent)
}

const (
	LinuxPackageDistStage = "dist"
)

func DockerfileDeclaresStage(dockerfilePath, stage string) bool {
	data, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return false
	}
	pattern := regexp.MustCompile(`(?im)^\s*FROM\s+.*\bAS\s+` + regexp.QuoteMeta(stage) + `(?:\s|$)`)
	return pattern.Match(data)
}

func CheckLinuxPackagesReleasable(packages []model.LinuxPackageSpec) error {
	for _, pkg := range packages {
		if !DockerfileDeclaresStage(pkg.DockerfilePath, LinuxPackageDistStage) {
			return fmt.Errorf("linux package %s declares no `dist` stage, so a release has no .deb files to publish: %s", pkg.Name, pkg.DockerfilePath)
		}
	}
	return nil
}

// ReleaseLinuxPackageDir is outside the worktree, which a release keeps clean.
func ReleaseLinuxPackageDir(projectRoot, version string) string {
	cacheDir, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(cacheDir) == "" {
		cacheDir = os.TempDir()
	}
	return filepath.Join(cacheDir, "erun", "release", filepath.Base(filepath.Clean(projectRoot)), version, "linux")
}
