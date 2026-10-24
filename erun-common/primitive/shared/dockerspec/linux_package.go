package dockerspec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LinuxPackageContextAtDir reports the linux package component at dir (a Dockerfile under linux/<name>).
func LinuxPackageContextAtDir(dir string) (LinuxPackageContext, bool, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || filepath.Base(filepath.Dir(dir)) != "linux" {
		return LinuxPackageContext{}, false, nil
	}

	dockerfilePath := filepath.Join(dir, "Dockerfile")
	info, err := os.Stat(dockerfilePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return LinuxPackageContext{}, false, nil
		}
		return LinuxPackageContext{}, false, err
	}
	if info.IsDir() {
		return LinuxPackageContext{}, false, nil
	}
	return LinuxPackageContext{Dir: dir, DockerfilePath: dockerfilePath}, true, nil
}

// IsLinuxPackageDockerfile reports whether a Dockerfile is a linux package component's, which is never an image.
func IsLinuxPackageDockerfile(dockerfilePath string) bool {
	dockerfilePath = strings.TrimSpace(dockerfilePath)
	if dockerfilePath == "" {
		return false
	}
	_, ok, err := LinuxPackageContextAtDir(filepath.Dir(filepath.Clean(dockerfilePath)))
	return err == nil && ok
}

// checkNotLinuxPackageDockerfile guards explicit-context paths; discovery never offers one.
func checkNotLinuxPackageDockerfile(dockerfilePath string) error {
	if !IsLinuxPackageDockerfile(dockerfilePath) {
		return nil
	}
	return fmt.Errorf("%s is a linux package, not an image: `erun build` runs its test stage and `erun build --release` publishes its .deb files to the GitHub release; it is never tagged or pushed", dockerfilePath)
}
