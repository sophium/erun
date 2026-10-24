package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func buildFingerprintScenarios(t *testing.T) {
	t.Run("real_run_configured_fingerprint_inspects_remote_manifest", testBuildRealRunConfiguredFingerprintInspectsRemoteManifest)
	t.Run("dry_run_with_dockerignore_drives_ignore_pattern_parser", testBuildDryRunWithDockerignoreDrivesIgnorePatternParser)
	t.Run("nested_gitignore_excludes_files_from_fingerprint", testBuildNestedGitignoreExcludesFilesFromFingerprint)
	t.Run("dry_run_new_version_rebuilds_version_stamped_image", testBuildDryRunNewVersionRebuildsVersionStampedImage)
	t.Run("real_run_with_existing_fingerprint_promotes_via_tag", testBuildRealRunWithExistingFingerprintPromotesViaTag)
	// A component whose Dockerfile declares a consumed `test` stage reports
	// each build's test outcome after it: `passed` when docker build ran the
	// graph, `reused (fingerprint ...)` when the fingerprint image was promoted
	// without a build. A component with no test stage says so instead. The
	// docker stub answers the test-stage daemon forwarder's inspect calls with
	// a stopped leftover (replaced) and then a running forwarder, so the real
	// run reaches the build without a daemon on the machine.
	for _, tc := range []struct {
		name         string
		imageInspect string
		runtime      string
	}{
		{name: "real_run_test_stage_reports_passed_after_building", imageInspect: "exit 1", runtime: "\nFROM alpine:3.22 AS runtime\nCOPY --from=test /out /out\n"},
		{name: "real_run_test_stage_reports_reused_when_promoted", imageInspect: "exit 0", runtime: "\nFROM alpine:3.22 AS runtime\nCOPY --from=test /out /out\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setup := env.New(t)
			fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
			mustWriteFile(t, filepath.Join(setup.Cwd, "erun-devops", "docker", "api", "Dockerfile"),
				"FROM --platform=$BUILDPLATFORM alpine:3.22 AS build\n"+
					"RUN mkdir /out\n"+
					"\n"+
					"FROM build AS test\n"+
					"RUN true\n"+
					tc.runtime)
			stubs := setup.Cwd + "/stubs"
			fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
				`case "$1" in`,
				`  image)`,
				`    case "$2" in`,
				`      inspect) ` + tc.imageInspect + ` ;;`,
				`      *) exit 0 ;;`,
				`    esac`,
				`    ;;`,
				`  inspect)`,
				`    case "$3" in`,
				`      *Labels*) echo "0 false" ;;`,
				`      *) echo true ;;`,
				`    esac`,
				`    ;;`,
				`  buildx)`,
				`    case "$2" in`,
				`      inspect) echo "Platforms: linux/arm64*, linux/amd64" ;;`,
				`      *) exit 0 ;;`,
				`    esac`,
				`    ;;`,
				`  *) exit 0 ;;`,
				`esac`,
			}, "\n"))
			envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
			envVars = append(envVars, stubHelmSilent(t, setup)...)
			result := erun.Run(t, []string{"build", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
			if result.ExitCode != 0 {
				t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
			}
			golden.Equal(t, "build/"+tc.name, normalize.Apply(result.Combined))
		})
	}
}

func testBuildRealRunConfiguredFingerprintInspectsRemoteManifest(t *testing.T) {
	// Stubbing `image inspect <fp-tag>` to fail (no local fingerprint) forces
	// the materialize step, which then runs `docker manifest inspect` and
	// `docker pull` against the configured source tag.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	fixture.SeedProjectK8sConfig(t, setup,
		"environments:\n"+
			"  local:\n"+
			"    docker:\n"+
			"      fingerprints:\n"+
			"        base: 0123456789abcdef\n",
	)
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1 $2" in`,
		// Local fingerprint missing → forces materialize path.
		`  "image inspect") exit 1 ;;`,
		// Remote manifest present → DockerManifestExists returns true.
		`  "manifest inspect") exit 0 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	envVars = append(envVars, stubHelmSilent(t, setup)...)
	result := erun.Run(t, []string{"build", "-v", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/real_run_configured_fingerprint_inspects_remote_manifest", normalize.Apply(result.Combined))
}

func testBuildDryRunWithDockerignoreDrivesIgnorePatternParser(t *testing.T) {
	// Seeds a .dockerignore with a mix of negation (!), comment, glob (*), and
	// directory patterns so the fingerprint walk exercises the ignore parser.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	dockerignore := strings.Join([]string{
		"# build context excludes",
		"node_modules/",
		"*.log",
		"!keep.log",
		"docs/**/*.md",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(setup.Cwd, ".dockerignore"), []byte(dockerignore), 0o644); err != nil {
		t.Fatalf("write .dockerignore: %v", err)
	}
	// Add a file that matches a pattern so the parser actually runs
	// against a non-empty context. Without commit it's still in the
	// build context for fingerprinting.
	if err := os.WriteFile(filepath.Join(setup.Cwd, "noisy.log"), []byte("ignored"), 0o644); err != nil {
		t.Fatalf("write noisy.log: %v", err)
	}
	fixture.RunGit(t, setup.Cwd, "add", ".dockerignore", "noisy.log")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add dockerignore + noisy file")
	result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	// Build should still trace docker build for both images, even
	// though the dockerignore parser fires during fingerprint walk.
	golden.Equal(t, "build/dry_run_with_dockerignore_drives_ignore_pattern_parser", normalize.Apply(result.Combined))
}

func testBuildNestedGitignoreExcludesFilesFromFingerprint(t *testing.T) {
	// A .gitignore at the root of a COPY'd directory must scope its patterns to
	// that subtree so matching files drop out of the fingerprint hash. Guards
	// the local-vs-CI drift where locally-built erun-cli/bin artifacts rolled
	// the devops fingerprint despite being .gitignore'd by a nested file.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	// Replace the default no-COPY api Dockerfile with one that COPYs a
	// subdirectory so the fingerprint walker actually descends.
	dockerfile := filepath.Join(setup.Cwd, "erun-devops", "docker", "api", "Dockerfile")
	mustWriteFile(t, dockerfile, "FROM alpine:3.22\nCOPY app/ /app/\n")
	appDir := filepath.Join(setup.Cwd, "app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatalf("mkdir app: %v", err)
	}
	mustWriteFile(t, filepath.Join(appDir, ".gitignore"), "secret.txt\n")
	mustWriteFile(t, filepath.Join(appDir, "tracked.txt"), "tracked v1\n")
	mustWriteFile(t, filepath.Join(appDir, "secret.txt"), "secret v1\n")
	fixture.RunGit(t, setup.Cwd, "add", "app/.gitignore", "app/tracked.txt", "erun-devops/docker/api/Dockerfile")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add app dir with nested gitignore")

	fp := func(label string) string {
		t.Helper()
		result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
		if result.ExitCode != 0 {
			t.Fatalf("%s: exit %d: %s", label, result.ExitCode, result.Combined)
		}
		return imageFingerprint(t, result.Combined, "api")
	}

	baseline := fp("baseline")
	mustWriteFile(t, filepath.Join(appDir, "secret.txt"), "secret v2\n")
	afterIgnoredChange := fp("after ignored change")
	if afterIgnoredChange != baseline {
		t.Fatalf("nested .gitignore not honored: fp moved from %s to %s after editing an ignored file", baseline, afterIgnoredChange)
	}
	mustWriteFile(t, filepath.Join(appDir, "tracked.txt"), "tracked v2\n")
	afterTrackedChange := fp("after tracked change")
	if afterTrackedChange == baseline {
		t.Fatalf("fingerprint did not move when a tracked file changed; got %s for both runs", baseline)
	}
}

func testBuildDryRunNewVersionRebuildsVersionStampedImage(t *testing.T) {
	// An image that bakes ERUN_VERSION into its content must not be published
	// at a new version by re-tagging the previous version's cached copy: the
	// binary inside would keep reporting the version before the tag it shipped
	// under. `stamped` consumes ERUN_VERSION, `api` does not. The first build
	// mints both fingerprints at 1.0.170; the docker stub then answers "present"
	// for exactly those two fp tags and "missing" for everything else, so the
	// second build at 1.0.171 sees the 1.0.170 cache and nothing more. stamped
	// must rebuild; api and base must still promote, so the cache keeps paying
	// for everything that carries no version.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	mustWriteFile(t, filepath.Join(setup.Cwd, "erun-devops", "docker", "stamped", "Dockerfile"),
		"FROM alpine:3.22\nARG ERUN_VERSION\nRUN echo \"${ERUN_VERSION}\" > /etc/erun-version\n")
	fixture.RunGit(t, setup.Cwd, "add", "erun-devops/docker/stamped/Dockerfile")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add version-stamped image")

	first := erun.Run(t, []string{"build", "--dry-run", "--version", "1.0.170"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if first.ExitCode != 0 {
		t.Fatalf("first build exit %d: %s", first.ExitCode, first.Combined)
	}
	cachedAPI := imageFingerprint(t, first.Combined, "api")
	cachedBase := imageFingerprint(t, first.Combined, "base")
	cachedStamped := imageFingerprint(t, first.Combined, "stamped")

	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$*" in`,
		`  "image inspect ghcr.io/sophium/api:fp-` + cachedAPI + `-"*) exit 0 ;;`,
		`  "image inspect ghcr.io/sophium/base:fp-` + cachedBase + `-"*) exit 0 ;;`,
		`  "image inspect ghcr.io/sophium/stamped:fp-` + cachedStamped + `-"*) exit 0 ;;`,
		`  *) exit 1 ;;`,
		`esac`,
	}, "\n"))
	second := erun.Run(t, []string{"build", "--dry-run", "--version", "1.0.171"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), fixture.StubEnv(stubs, "docker")...)})
	if second.ExitCode != 0 {
		t.Fatalf("second build exit %d: %s", second.ExitCode, second.Combined)
	}
	// Normalization collapses every fingerprint to <HEX16>, so the golden
	// cannot tell the 1.0.170 cache tag from a fresh one — assert the pinned
	// hashes on the raw trace.
	if staleTag := "ghcr.io/sophium/stamped:fp-" + cachedStamped + "-"; strings.Contains(second.Combined, staleTag) {
		t.Fatalf("1.0.171 re-tagged the 1.0.170 stamped image (%s), so the published image would report 1.0.170:\n%s", staleTag, second.Combined)
	}
	if promoted := "docker tag ghcr.io/sophium/api:fp-" + cachedAPI + "-amd64 ghcr.io/sophium/api:1.0.171-amd64"; !strings.Contains(second.Combined, promoted) {
		t.Fatalf("expected the version-free api image to still promote across the version change (%s):\n%s", promoted, second.Combined)
	}
	golden.Equal(t, "build/dry_run_new_version_rebuilds_version_stamped_image", normalize.Apply(second.Combined))
}

func testBuildRealRunWithExistingFingerprintPromotesViaTag(t *testing.T) {
	// When `docker image inspect <fp-tag>` succeeds, the build re-tags the
	// existing fingerprint image to the version tag instead of running docker
	// build. The stub makes `image inspect` always succeed; asserts no
	// `docker build` appears and `docker tag` re-tagging from the fingerprint
	// does.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	envVars = append(envVars, stubHelmSilent(t, setup)...)
	result := erun.Run(t, []string{"build", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/real_run_with_existing_fingerprint_promotes_via_tag", normalize.Apply(result.Combined))
}
