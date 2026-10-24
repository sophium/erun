package e2e

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
)

func TestE2EDockerBuildNestsUnderTheCPUCapWhenOneApplies(t *testing.T) {
	suite := model.E2ESuite{Component: "console", Dockerfile: "team-devops/e2e/console/Dockerfile"}
	args := strings.Join(e2eDockerBuildArgs(suite, "1.2.3", "https://console.example.test", nil, nil, "/docker/erun-build-cpu-cap-pod"), " ")
	if !strings.Contains(args, "--cgroup-parent /docker/erun-build-cpu-cap-pod -f team-devops/e2e/console/Dockerfile .") {
		t.Fatalf("an in-pod e2e run should carry --cgroup-parent, got %q", args)
	}
	if bare := strings.Join(e2eDockerBuildArgs(suite, "1.2.3", "https://console.example.test", nil, nil, ""), " "); strings.Contains(bare, "--cgroup-parent") {
		t.Fatalf("a host e2e run carries no --cgroup-parent, got %q", bare)
	}
}
