package integration

import (
	"path/filepath"
	"regexp"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// imageFingerprint reads the fingerprint-cache tag an image resolved to from the
// raw trace, which output normalization would otherwise collapse to <HEX16>.
func imageFingerprint(t testing.TB, out, image string) string {
	t.Helper()
	re := regexp.MustCompile(`ghcr\.io/sophium/` + regexp.QuoteMeta(image) + `:fp-([0-9a-f]{16})-amd64`)
	match := re.FindStringSubmatch(out)
	if match == nil {
		t.Fatalf("no fingerprint tag for %s in trace:\n%s", image, out)
	}
	return match[1]
}

// chartPackageVersion reads a chart's published version from the raw trace,
// which output normalization would otherwise collapse to <VERSION>.
func chartPackageVersion(t testing.TB, out, chart string) string {
	t.Helper()
	re := regexp.MustCompile(`helm package ` + regexp.QuoteMeta(chart) + ` --version (\S+)`)
	m := re.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no `helm package %s --version` line in output:\n%s", chart, out)
	}
	return m[1]
}

func TestBuild(t *testing.T) {
	t.Parallel()
	t.Run("help", testBuildHelp)
	buildLayoutScenarios(t)
	buildPlatformScenarios(t)
	buildDockerSecretScenarios(t)
	buildOutputAndScriptScenarios(t)
	buildFingerprintScenarios(t)
	buildDeployFlagScenarios(t)
	buildDindScenarios(t)
	buildLinuxPackageScenarios(t)
	buildBaseOrderingScenarios(t)
	buildReleasePushScenarios(t)
	buildReportScenarios(t)
	t.Run("e2e_is_not_a_build_flag", testBuildE2eIsNotABuildFlag)
}

func testBuildHelp(t *testing.T) {
	setup := env.New(t)
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	result := erun.Run(t, []string{"build", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/help", normalize.Apply(result.Combined))
}

func testBuildE2eIsNotABuildFlag(t *testing.T) {
	// e2e suites run only through `erun e2e`, never as part of a build:
	// --e2e is refused as an unknown flag rather than silently ignored.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"build", "--e2e", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected --e2e to be refused as an unknown build flag, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "build/e2e_is_not_a_build_flag", normalize.Apply(result.Combined))
}

// stubDockerNoLocalImages makes every docker invocation a clean "No such image"
// miss. Dry-run build/deploy scenarios consult docker as decision input
// (fingerprint inspects, manifest probes) even though they mutate nothing;
// without the stub they silently depend on a host docker CLI and fail in
// docker-less environments such as the image build's test stage.
// dockerSecretFixtureValue stands in for a build secret's credential in the
// docker.secrets scenarios. It is a fixture literal, not a credential: the
// scenarios that set it assert the value never reaches the build's output, so
// its only job is to be recognisable if one of them ever leaks it.
const dockerSecretFixtureValue = "fixture-value-must-never-be-traced"

func stubDockerNoLocalImages(t *testing.T, setup env.Setup) []string {
	t.Helper()
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryAdvanced(t, stubs, "docker", fixture.StubBinarySpec{ExitCode: 1})
	return fixture.StubEnv(stubs, "docker")
}

// stubDockerArm64Daemon is a docker stub with no local images whose daemon
// answers `docker version` as linux/arm64, the platform a linux package's
// recorded pass is keyed by.
func stubDockerArm64Daemon(t *testing.T, setup env.Setup) []string {
	t.Helper()
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryWithScript(t, stubs, "docker", `if [ "$1" = version ]; then echo linux/arm64; exit 0; fi`+"\nexit 1")
	return fixture.StubEnv(stubs, "docker")
}

// dockerStubRecordingTo is a docker stub that appends its argv to calls and
// answers `docker version` as a linux/arm64 daemon, the platform a package's
// recorded pass is keyed by.
func dockerStubRecordingTo(calls string) string {
	return `echo "$*" >> '` + calls + `'` + "\n" + `if [ "$1" = version ]; then echo linux/arm64; fi`
}

// stubHelmSilent adds a no-op helm stub so a real-run build that packages the
// repo's k8s/* charts does not shell out to a real helm; returns the
// ERUN_HELM_BIN env pair to append to the scenario's env.
func stubHelmSilent(t *testing.T, setup env.Setup) []string {
	t.Helper()
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinary(t, stubs, "helm", "")
	return fixture.StubEnv(stubs, "helm")
}

// Linux package fixtures: one with a test stage, one without. Both export the
// .deb files at the root of a scratch `dist` stage, the shape a release uploads.
const (
	testedLinuxPackageDockerfile = "" +
		"FROM --platform=$BUILDPLATFORM golang:1.26.0 AS build\n" +
		"RUN go build -o /out/erun-app ./...\n" +
		"\n" +
		"FROM build AS test\n" +
		"RUN go test ./...\n" +
		"\n" +
		"FROM test AS package\n" +
		"ARG ERUN_VERSION\n" +
		"RUN dpkg-deb --build /pkg /erun-desktop_${ERUN_VERSION}.deb\n" +
		"\n" +
		"FROM scratch AS dist\n" +
		"COPY --from=package /*.deb /\n"
	untestedLinuxPackageDockerfile = "" +
		"FROM --platform=$BUILDPLATFORM golang:1.26.0 AS build\n" +
		"RUN go build -o /out/erun ./...\n" +
		"\n" +
		"FROM build AS package\n" +
		"ARG ERUN_VERSION\n" +
		"RUN dpkg-deb --build /pkg /erun_${ERUN_VERSION}.deb\n" +
		"\n" +
		"FROM scratch AS dist\n" +
		"COPY --from=package /*.deb /\n"
)
