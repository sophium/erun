package eruncommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The grant is decided from the Dockerfile's own content, so every case here
// has to write a real one: a spec pointing at a path that does not exist
// exercises the no-test-stage arm whatever the case is about.
func entitlementDockerfilePath(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Dockerfile")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	return path
}

func entitlementArgv(t *testing.T, content string) []string {
	t.Helper()
	return dockerBuildArgs(
		DockerBuildSpec{
			DockerfilePath: entitlementDockerfilePath(t, content),
			Image:          DockerImageReference{Tag: "ghcr.io/acme/widget:1.2.3"},
		},
		"linux/amd64",
	)
}

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
	argv := entitlementArgv(t, gateDockerfileContent)

	if !grantsEntitlement(argv, "network.host") {
		t.Fatalf(
			"docker build argv does not grant the network.host entitlement, so a test stage using RUN --network=host fails at LLB load (argv: %s)",
			strings.Join(argv, " "),
		)
	}
}

// The grant is scoped to the build that has somewhere to run tests. A
// production Dockerfile with no test stage must keep BuildKit's default deny:
// the entitlement hands a build step the *builder's* network namespace, which
// is this environment pod's, so granting it to a build that cannot use it
// broadens a real capability nobody asked to broaden. This is the arm that says
// the scoping is real rather than a comment.
func TestDockerBuildWithoutTestStageKeepsTheDefaultDeny(t *testing.T) {
	argv := entitlementArgv(t, "FROM alpine:3.22\nRUN apk add --no-cache curl\n")

	if grantsEntitlement(argv, "network.host") {
		t.Fatalf(
			"a Dockerfile with no test stage was granted network.host, so every erun-issued build lifts BuildKit's default deny (argv: %s)",
			strings.Join(argv, " "),
		)
	}
}

// The grant must not be positional: `--allow` takes its value as a separate
// token, so a rewrite that emits the two apart -- or that emits a bare
// `--allow` with nothing after it -- would still satisfy a substring check
// while granting nothing. Pairing them is the whole assertion.
func TestDockerBuildGrantIsPairedWithItsValue(t *testing.T) {
	argv := entitlementArgv(t, gateDockerfileContent)

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
