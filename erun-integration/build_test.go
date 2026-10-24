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

// imageFingerprint reads the fingerprint tag from the raw, unnormalized trace.
func imageFingerprint(t testing.TB, out, image string) string {
	t.Helper()
	re := regexp.MustCompile(`ghcr\.io/sophium/` + regexp.QuoteMeta(image) + `:fp-([0-9a-f]{16})-amd64`)
	match := re.FindStringSubmatch(out)
	if match == nil {
		t.Fatalf("no fingerprint tag for %s in trace:\n%s", image, out)
	}
	return match[1]
}

// chartPackageVersion reads the chart version from the raw, unnormalized trace.
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

// dockerSecretFixtureValue is a fixture literal the scenarios assert never leaks.
const dockerSecretFixtureValue = "fixture-value-must-never-be-traced"

// stubDockerNoLocalImages keeps dry-run scenarios from depending on a host docker CLI.
func stubDockerNoLocalImages(t *testing.T, setup env.Setup) []string {
	t.Helper()
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryAdvanced(t, stubs, "docker", fixture.StubBinarySpec{ExitCode: 1})
	return fixture.StubEnv(stubs, "docker")
}

// stubDockerArm64Daemon answers `docker version` as linux/arm64, the platform
// a package's recorded pass is keyed by.
func stubDockerArm64Daemon(t *testing.T, setup env.Setup) []string {
	t.Helper()
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryWithScript(t, stubs, "docker", `if [ "$1" = version ]; then echo linux/arm64; exit 0; fi`+"\nexit 1")
	return fixture.StubEnv(stubs, "docker")
}

func dockerStubRecordingTo(calls string) string {
	return `echo "$*" >> '` + calls + `'` + "\n" + `if [ "$1" = version ]; then echo linux/arm64; fi`
}

func stubHelmSilent(t *testing.T, setup env.Setup) []string {
	t.Helper()
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinary(t, stubs, "helm", "")
	return fixture.StubEnv(stubs, "helm")
}

// Linux package fixtures export .deb files at the root of a scratch `dist`
// stage, the shape a release uploads.
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
