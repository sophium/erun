package eruncommon

import (
	"strings"
	"testing"
)

// A component's test stage may need a real container runtime -- the
// environment's own dind -- to start a fixture. BuildKit default-denies
// `RUN --network=host`, so without this grant such a Dockerfile does not build
// at all, and the documented expectation that component tests belong in that
// component's test stage becomes unsatisfiable rather than merely unfollowed.
//
// The argv is the earliest point the grant can be observed: the refusal it
// lifts happens at LLB load inside the daemon, so a test that does not read the
// argv cannot tell a build that grants the entitlement from one that does not
// until a real build runs.
func TestDockerBuildGrantsHostNetworkEntitlement(t *testing.T) {
	argv := dockerBuildArgs(
		DockerBuildSpec{
			DockerfilePath: "Dockerfile",
			Image:          DockerImageReference{Tag: "ghcr.io/acme/widget:1.2.3"},
		},
		"linux/amd64",
	)

	if !grantsEntitlement(argv, "network.host") {
		t.Fatalf(
			"docker build argv does not grant the network.host entitlement, so a test stage using RUN --network=host fails at LLB load (argv: %s)",
			strings.Join(argv, " "),
		)
	}
}

// The grant must not be positional: `--allow` takes its value as a separate
// token, so a rewrite that emits the two apart -- or that emits a bare
// `--allow` with nothing after it -- would still satisfy a substring check
// while granting nothing. Pairing them is the whole assertion.
func TestDockerBuildGrantIsPairedWithItsValue(t *testing.T) {
	argv := dockerBuildArgs(
		DockerBuildSpec{
			DockerfilePath: "Dockerfile",
			Image:          DockerImageReference{Tag: "ghcr.io/acme/widget:1.2.3"},
		},
		"linux/amd64",
	)

	for i, arg := range argv {
		if arg != "--allow" {
			continue
		}
		if i == len(argv)-1 {
			t.Fatalf("docker build argv ends on a bare --allow with no entitlement value (argv: %s)", strings.Join(argv, " "))
		}
	}
}

func grantsEntitlement(argv []string, want string) bool {
	for i, arg := range argv {
		if arg == "--allow" && i+1 < len(argv) && argv[i+1] == want {
			return true
		}
	}
	return false
}
