package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

var apiFingerprintRE = regexp.MustCompile(`ghcr\.io/sophium/api:fp-([0-9a-f]{16})-amd64`)

func TestBuild(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"build", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_from_devops_cwd", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		result := erun.Run(t, []string{"build", "--dry-run", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "build/dry_run_from_devops_cwd", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_no_devops_recommends_build_env_skill", func(t *testing.T) {
		// #534: erun build in a project with no <tenant>-devops module emits a
		// one-line advisory pointing at the erun-build-env skill. The advisory
		// fires from ResolveBuildExecution whenever a build runs without a devops
		// build environment — here a project build.sh registers and runs the
		// build command and the build succeeds via the script, so the tip is
		// emitted even though the build itself does not fail.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		if err := os.WriteFile(filepath.Join(setup.Cwd, "build.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write build.sh: %v", err)
		}
		fixture.RunGit(t, setup.Cwd, "add", "build.sh")
		fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add build script")
		result := erun.Run(t, []string{"build", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/dry_run_no_devops_recommends_build_env_skill", normalize.Apply(result.Combined))
	})

	t.Run("no_build_registry_errors", func(t *testing.T) {
		// A project registry list that marks no build registry cannot build:
		// `erun build` fails fast with the "no build registry" contract message
		// instead of silently falling back to the default registry. The list is
		// valid (from+to, deploy on to) — it simply omits the build role.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		fixture.SeedProjectK8sConfig(t, setup,
			"containerregistries:\n"+
				"    - registry: ghcr.io/sophium\n"+
				"      roles: [from]\n"+
				"    - registry: registry.internal/team\n"+
				"      roles: [to, deploy]\n",
		)
		result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a project with no build registry, got 0: %s", result.Combined)
		}
		golden.Equal(t, "build/no_build_registry_errors", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_from_release_repo_traces_docker_builds", func(t *testing.T) {
		// Exercises build.go shorthand from a project root with the
		// erun-devops release-shape layout: --dry-run must trace one
		// docker build per discovered Dockerfile, with the resolved
		// context dir, dockerfile path, image tag, ERUN_VERSION build
		// arg, and the fingerprint tag. Replaces the unit-level
		// coverage of dockerBuildArgs, ResolveDockerBuildContextDirForProject,
		// and the incremental fingerprint trace in erun-common.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/dry_run_from_release_repo_traces_docker_builds", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_release_includes_release_and_build_traces", func(t *testing.T) {
		// Exercises build.go --release flag: --dry-run must combine the
		// release plan (sync, version write, tag) with the docker build
		// trace using the release-resolved version.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		result := erun.Run(t, []string{"build", "--release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/dry_run_release_includes_release_and_build_traces", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_release_publishes_runtime_chart", func(t *testing.T) {
		// A release build whose release root carries the canonical
		// erun-devops chart must publish it as a release artifact right
		// after the image pushes (helm package + helm push to
		// oci://<registry>/charts) and then verify the pushed chart is
		// fetchable (helm pull) — image and chart are one contract (#505).
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		chartDir := filepath.Join(setup.Cwd, "erun-devops", "k8s", "erun-devops")
		if err := os.MkdirAll(chartDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", chartDir, err)
		}
		mustWriteFile(t, filepath.Join(chartDir, "Chart.yaml"), "apiVersion: v2\nname: erun-devops\ndescription: ERun DevOps\nversion: 0.1.0\nappVersion: 0.1.0\n")
		// Seed the runtime image build context too: a real release builds and
		// pushes the erun-devops image, and push publishes the erun-devops chart
		// in lockstep with that image.
		runtimeDockerDir := filepath.Join(setup.Cwd, "erun-devops", "docker", "erun-devops")
		if err := os.MkdirAll(runtimeDockerDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", runtimeDockerDir, err)
		}
		mustWriteFile(t, filepath.Join(runtimeDockerDir, "Dockerfile"), "FROM alpine:3.22\n")
		fixture.RunGit(t, setup.Cwd, "add", ".")
		fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add runtime chart and image")
		result := erun.Run(t, []string{"build", "--release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/dry_run_release_publishes_runtime_chart", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_configured_fingerprint_traces_pull_and_tag", func(t *testing.T) {
		// Exercises docker.fingerprints config: when an image name matches a
		// configured fingerprint, the materialize step traces docker manifest
		// inspect / docker pull / docker tag for each platform before the
		// regular fingerprint inspect runs. The dry-run does not actually
		// pull; it traces the would-be commands so the maintainer can audit.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		fixture.SeedProjectK8sConfig(t, setup,
			"environments:\n"+
				"  local:\n"+
				"    docker:\n"+
				"      fingerprints:\n"+
				"        base: 0123456789abcdef\n",
		)
		result := erun.Run(t, []string{"build", "--dry-run", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/dry_run_configured_fingerprint_traces_pull_and_tag", normalize.Apply(result.Combined))
	})

	t.Run("real_run_via_docker_stub_drives_multi_platform_build", func(t *testing.T) {
		// Exercises eruncommon/build_docker_commands.go DockerImageBuilder,
		// runMultiPlatformBuild, runDockerBuildOnce, and tagFingerprintAfterBuild.
		// Stubs `docker` to exit 0 for every invocation so the build flow
		// runs to completion across both platforms without touching a real
		// daemon. Asserts the user-facing "Built" / "Tagged" lines appear.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
			`case "$1" in`,
			`  image)`,
			`    case "$2" in`,
			`      inspect) exit 1 ;;`,
			`      *) exit 0 ;;`,
			`    esac`,
			`    ;;`,
			// The multi-arch daemon-capability preflight (issue #645) runs
			// `docker buildx inspect` before building; report both required
			// platforms so it passes. The trailing `*` on the node default is
			// realistic buildx output and exercises the marker-stripping parse.
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
		result := erun.Run(t, []string{"build", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/real_run_via_docker_stub_drives_multi_platform_build", normalize.Apply(result.Combined))
	})

	t.Run("real_run_fails_when_daemon_cannot_build_required_platform", func(t *testing.T) {
		// Exercises the multi-arch daemon-capability preflight
		// (verifyDockerBuildPlatforms in build_platform_preflight.go, issue
		// #645). erun always builds linux/amd64 + linux/arm64, so before
		// shelling `docker build` per platform it runs `docker buildx inspect`
		// and fails fast with a direct, actionable error when the daemon has
		// no emulator for a required platform — instead of the opaque
		// per-platform `docker build` failure. The stub reports only
		// linux/amd64, so linux/arm64 is unbuildable regardless of host arch
		// (the stub controls the platform list). This is a real-run scenario
		// because the preflight guards the real executor, not the dry-run plan.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
			`case "$1" in`,
			`  image)`,
			`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
			`  buildx)`,
			`    case "$2" in inspect) echo "Platforms: linux/amd64" ;; *) exit 0 ;; esac ;;`,
			`  *) exit 0 ;;`,
			`esac`,
		}, "\n"))
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
		result := erun.Run(t, []string{"build", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected build to fail when the daemon cannot build a required platform; got exit 0:\n%s", result.Combined)
		}
		golden.Equal(t, "build/real_run_fails_when_daemon_cannot_build_required_platform", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_no_incremental_skips_fingerprint_short_circuit", func(t *testing.T) {
		// Exercises the --no-incremental branch in build orchestration
		// (BuildExecution.NoIncremental, BuildOrderForRefactoredFingerprints
		// path). With --no-incremental, the build trace must run
		// `docker build` for every image even when a fingerprint tag
		// exists — there's no `docker image inspect` short-circuit and
		// no `(skipping)` lines.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		result := erun.Run(t, []string{"build", "--dry-run", "--no-incremental"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/dry_run_no_incremental_skips_fingerprint_short_circuit", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_project_build_script_traces_script_invocation", func(t *testing.T) {
		// Exercises eruncommon/project_build_script.go (HasProjectBuildScript,
		// resolveProjectRootBuildScript) + build_docker_commands.go
		// (runScriptSpec, scriptTraceCommand, buildScriptEnv): when a
		// build.sh exists at the project root, the build flow calls the
		// script instead of running docker builds. Dry-run traces the
		// resolved script path with ERUN_BUILD_VERSION and skips the
		// docker build chain.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		if err := os.WriteFile(filepath.Join(setup.Cwd, "build.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write build.sh: %v", err)
		}
		fixture.RunGit(t, setup.Cwd, "add", "build.sh")
		fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add build script")
		result := erun.Run(t, []string{"build", "--dry-run", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/dry_run_with_project_build_script_traces_script_invocation", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_disable_build_script_ignores_project_build_sh", func(t *testing.T) {
		// #533: an env with disablebuildscript: true makes erun build ignore the
		// project build.sh and resolve docker/release contexts directly. With no
		// docker context the build ends at the no-buildable-context error rather
		// than tracing ./build.sh (which dry_run_with_project_build_script does).
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		envCfg := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		data, err := os.ReadFile(envCfg)
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		if err := os.WriteFile(envCfg, append(data, []byte("disablebuildscript: true\n")...), 0o644); err != nil {
			t.Fatalf("write env config: %v", err)
		}
		if err := os.WriteFile(filepath.Join(setup.Cwd, "build.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write build.sh: %v", err)
		}
		result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit (build.sh ignored, no docker context), got 0: %s", result.Combined)
		}
		golden.Equal(t, "build/dry_run_disable_build_script_ignores_project_build_sh", normalize.Apply(result.Combined))
	})

	t.Run("real_run_with_project_build_script_executes_script", func(t *testing.T) {
		// Real-run companion to dry_run_with_project_build_script_traces_script_invocation:
		// runs the build flow without --dry-run so eruncommon.BuildScriptRunner
		// actually invokes ./build.sh. Asserts the command exits 0 and a
		// marker file the script writes appears, confirming the script
		// process actually ran.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		marker := filepath.Join(setup.Cwd, "build-script-marker")
		scriptBody := "#!/bin/sh\nprintf 'ran with %s\\n' \"$ERUN_BUILD_VERSION\" > '" + marker + "'\nexit 0\n"
		if err := os.WriteFile(filepath.Join(setup.Cwd, "build.sh"), []byte(scriptBody), 0o755); err != nil {
			t.Fatalf("write build.sh: %v", err)
		}
		fixture.RunGit(t, setup.Cwd, "add", "build.sh")
		fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add build script")
		result := erun.Run(t, []string{"build"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		body, err := os.ReadFile(marker)
		if err != nil {
			t.Fatalf("read marker: %v", err)
		}
		if !strings.HasPrefix(string(body), "ran with ") {
			t.Errorf("expected marker prefix 'ran with ', got: %q", body)
		}
	})

	t.Run("real_run_configured_fingerprint_inspects_remote_manifest", func(t *testing.T) {
		// Exercises pullAndTagConfiguredFingerprint + DockerManifestExists
		// on the materialize-configured-fingerprint path. Stubs docker so
		// `image inspect <fp-tag>` fails (no local fingerprint), forcing
		// the materialize step, which then runs `docker manifest inspect`
		// and `docker pull` against the configured source tag.
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
		result := erun.Run(t, []string{"build", "-v", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/real_run_configured_fingerprint_inspects_remote_manifest", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_dockerignore_drives_ignore_pattern_parser", func(t *testing.T) {
		// Exercises eruncommon/build_incremental.go ignoreSet parser:
		// parseIgnoreData, patternMatchesPath, globMatch, globToRegex.
		// Seeds .dockerignore in the project root with a mix of negation
		// (!), comment, glob (*), and directory patterns. The fingerprint
		// computation walks the build context, calls loadIgnoreFile,
		// which parses these patterns and applies them.
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
	})

	t.Run("nested_gitignore_excludes_files_from_fingerprint", func(t *testing.T) {
		// Exercises loadNestedGitignores in erun-common/build_incremental.go:
		// a .gitignore at the root of a COPY'd directory must scope its
		// patterns to that subtree so files matching the nested patterns
		// drop out of the fingerprint hash. This guards the local-vs-CI
		// drift seen on #359 where locally-built erun-cli/bin artifacts
		// were rolling the devops fingerprint despite being .gitignore'd
		// by a nested file. Verified by comparing fingerprints across
		// three runs:
		//   1. baseline with ignored "secret.txt" content X
		//   2. ignored content rewritten to Y → fingerprint must stay equal
		//   3. tracked "tracked.txt" content rewritten → fingerprint must move
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
			match := apiFingerprintRE.FindStringSubmatch(result.Combined)
			if match == nil {
				t.Fatalf("%s: api fingerprint not found in trace:\n%s", label, result.Combined)
			}
			return match[1]
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
	})

	t.Run("real_run_with_existing_fingerprint_promotes_via_tag", func(t *testing.T) {
		// Exercises promoteDockerImage + runDockerTag promote path:
		// when `docker image inspect <fp-tag>` returns success, the
		// build flow sets DockerBuildSpec.Promote=true and re-tags
		// the existing fingerprint image to the version tag instead
		// of running docker build. Stubs docker so `image inspect`
		// always succeeds, then asserts no `docker build` calls
		// appear and `docker tag` re-tagging from the fingerprint
		// does.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "docker", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
		result := erun.Run(t, []string{"build", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/real_run_with_existing_fingerprint_promotes_via_tag", normalize.Apply(result.Combined))
	})

	t.Run("real_run_release_pushes_multi_platform_manifest", func(t *testing.T) {
		// Exercises pushMultiPlatformImage (and the manifest create+push
		// path) plus runDockerPushOnce in real-run release mode. The
		// release branch sets DockerBuildSpec.Push=true for release-tagged
		// images, which drives runMultiPlatformBuild's push branch.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
			`case "$1" in`,
			`  image)`,
			`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
			// Multi-arch capability preflight (issue #645): report both required
			// platforms so `docker buildx inspect` passes.
			`  buildx)`,
			`    case "$2" in inspect) echo "Platforms: linux/amd64, linux/arm64" ;; *) exit 0 ;; esac ;;`,
			`  *) exit 0 ;;`,
			`esac`,
		}, "\n"))
		// Release flow runs git tag/push; stub git verb-by-verb so the
		// release stage succeeds without touching a real remote.
		fixture.StubBinary(t, stubs, "git", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
		// Keep real git for SeedReleaseRepo's repo setup and use the stub
		// only for erun's release operations: the production code resolves
		// `git` via common.Command which honors ERUN_GIT_BIN. The repo
		// already exists via the seed.
		envVars = append(envVars, fixture.StubEnv(stubs, "git")...)
		result := erun.Run(t, []string{"build", "--release", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/real_run_release_pushes_multi_platform_manifest", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_build_deploy_resolves_docker_target_deploy_specs", func(t *testing.T) {
		// Exercises runBuildCommand's --deploy branch and the docker-target
		// deploy resolution chain in erun-common/deploy.go:
		// ResolveCurrentDeploySpecsForDockerTarget ->
		// resolveDeployTargetForDockerTarget (project root from git,
		// environment from the tenant env whose repopath matches, tenant via
		// resolveProjectTenantForRoot + loadDefaultTenant) ->
		// ResolveCurrentDeploySpecs. The build phase builds and pushes the
		// cwd image (buildAndPushDeployDockerImages), then RunDeploySpec
		// rolls the chart out with the explicit --version so both phases
		// resolve the same deterministic tag.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDevopsRuntimeDockerfile(t, setup, "team")
		fixture.SeedGitRepo(t, setup.Cwd)
		dockerDir := filepath.Join(setup.Cwd, "team-devops", "docker", "team-devops")
		result := erun.Run(t, []string{"build", "--deploy", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: dockerDir, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/dry_run_build_deploy_resolves_docker_target_deploy_specs", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_linux_package_from_component_dir", func(t *testing.T) {
		// Exercises the explicit linux-package build path: from inside
		// linux/<component>, `erun build` resolves the dir's build.sh as the
		// build (LinuxPackageContextAtDir → ResolveCurrentLinuxBuildScripts)
		// and dry-run traces the ./build.sh invocation with the version
		// argument. ERUN_HOST_OS_OVERRIDE pins the host to linux and a
		// dpkg-deb stub on PATH satisfies LinuxPackageBuildsSupported, so
		// the golden is identical on mac/CI hosts.
		setup := env.New(t)
		pkgDir := filepath.Join(setup.Cwd, "team-devops", "linux", "erun-host")
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", pkgDir, err)
		}
		if err := os.WriteFile(filepath.Join(pkgDir, "build.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write build.sh: %v", err)
		}
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "dpkg-deb", "")
		envVars := append(setup.Env(), "ERUN_HOST_OS_OVERRIDE=linux")
		envVars = append(envVars, "PATH="+stubs+":"+os.Getenv("PATH"))
		result := erun.Run(t, []string{"build", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: pkgDir, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/dry_run_linux_package_from_component_dir", normalize.Apply(result.Combined))
	})

	t.Run("real_run_linux_packages_from_linux_dir", func(t *testing.T) {
		// Exercises ResolveLinuxPackageContextsAtDir + the script execution
		// leg of runBuildExecution: from the linux/ parent dir every
		// component's build.sh runs for real with ERUN_BUILD_VERSION set. The
		// scripts record their invocation to marker files (side effect
		// outside the captured streams). Host pinned to linux with a
		// dpkg-deb stub as in the dry-run scenario.
		setup := env.New(t)
		linuxDir := filepath.Join(setup.Cwd, "team-devops", "linux")
		pkgDir := filepath.Join(linuxDir, "erun-host")
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", pkgDir, err)
		}
		marker := filepath.Join(setup.Cwd, "build-ran")
		script := "#!/bin/sh\nprintf '%s' \"$ERUN_BUILD_VERSION\" > '" + marker + "'\nexit 0\n"
		if err := os.WriteFile(filepath.Join(pkgDir, "build.sh"), []byte(script), 0o755); err != nil {
			t.Fatalf("write build.sh: %v", err)
		}
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "dpkg-deb", "")
		envVars := append(setup.Env(), "ERUN_HOST_OS_OVERRIDE=linux")
		envVars = append(envVars, "PATH="+stubs+":"+os.Getenv("PATH"))
		result := erun.Run(t, []string{"build", "--version", "1.0.0"}, erun.RunOptions{Cwd: linuxDir, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/real_run_linux_packages_from_linux_dir", normalize.Apply(result.Combined))
		ran, err := os.ReadFile(marker)
		if err != nil {
			t.Fatalf("expected build.sh to run and write its marker: %v", err)
		}
		if string(ran) != "1.0.0" {
			t.Errorf("expected build.sh to receive ERUN_BUILD_VERSION, got %q", ran)
		}
	})

	t.Run("dry_run_project_root_walks_devops_linux_dir", func(t *testing.T) {
		// Exercises ResolveCurrentLinuxPackageContexts' devops-walk arms
		// (resolveCurrentDevopsLinuxDir → findDevopsLinuxDirs): from the
		// project root the linux module is discovered during command
		// registration, while the build itself stays scoped to the docker
		// contexts (explicit linux builds only fire inside linux/ dirs).
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDevopsRuntimeDockerfile(t, setup, "team")
		fixture.SeedGitRepo(t, setup.Cwd)
		pkgDir := filepath.Join(setup.Cwd, "team-devops", "linux", "erun-host")
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", pkgDir, err)
		}
		if err := os.WriteFile(filepath.Join(pkgDir, "build.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write build.sh: %v", err)
		}
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinary(t, stubs, "dpkg-deb", "")
		envVars := append(setup.Env(), stubDockerNoLocalImages(t, setup)...)
		envVars = append(envVars, "ERUN_HOST_OS_OVERRIDE=linux")
		envVars = append(envVars, "PATH="+stubs+":"+os.Getenv("PATH"))
		result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/dry_run_project_root_walks_devops_linux_dir", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_build_deploy_default_tenant_breaks_project_tie", func(t *testing.T) {
		// Exercises resolveProjectTenantForRoot's default-tenant tie-break:
		// two tenants share the same project root, and `build --deploy`
		// (which infers the tenant from the project) must pick the
		// configured default tenant instead of erroring on the ambiguity.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDevopsRuntimeDockerfile(t, setup, "team")
		fixture.SeedGitRepo(t, setup.Cwd)
		// A second tenant claiming the same project root, with no envs so
		// it cannot interfere with environment resolution. Named to sort
		// after "team" so the environment legacy fallback (which walks
		// tenants alphabetically) resolves team/dev first.
		otherDir := filepath.Join(setup.ConfigHome, "erun", "zz-extra")
		if err := os.MkdirAll(otherDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", otherDir, err)
		}
		if err := os.WriteFile(filepath.Join(otherDir, "config.yaml"),
			[]byte("projectroot: "+setup.Cwd+"\nname: zz-extra\n"), 0o644); err != nil {
			t.Fatalf("zz-extra tenant cfg: %v", err)
		}
		dockerDir := filepath.Join(setup.Cwd, "team-devops", "docker", "team-devops")
		result := erun.Run(t, []string{"build", "--deploy", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: dockerDir, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/dry_run_build_deploy_default_tenant_breaks_project_tie", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_build_deploy_ambiguous_tenants_error", func(t *testing.T) {
		// Exercises resolveProjectTenantForRoot's ambiguity guard: two
		// tenants share the project root and the default tenant points
		// elsewhere, so the inferred-tenant deploy must fail with "multiple
		// tenants are configured for project" rather than guessing.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedDevopsRuntimeDockerfile(t, setup, "team")
		fixture.SeedGitRepo(t, setup.Cwd)
		// The second tenant genuinely owns the same project root: cwd→tenant
		// matching is now via each tenant's envs' localRepoPath (#549), so the
		// other tenant needs an env recording this cwd, not just a bare
		// tenant-level projectroot, for the ambiguity to hold.
		otherEnvDir := filepath.Join(setup.ConfigHome, "erun", "other", "dev")
		if err := os.MkdirAll(otherEnvDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", otherEnvDir, err)
		}
		if err := os.WriteFile(filepath.Join(setup.ConfigHome, "erun", "other", "config.yaml"),
			[]byte("name: other\ndefaultenvironment: dev\n"), 0o644); err != nil {
			t.Fatalf("other tenant cfg: %v", err)
		}
		if err := os.WriteFile(filepath.Join(otherEnvDir, "config.yaml"),
			[]byte("name: dev\nlocalrepopath: "+setup.Cwd+"\ntype: local-agent\n"), 0o644); err != nil {
			t.Fatalf("other env cfg: %v", err)
		}
		if err := os.WriteFile(filepath.Join(setup.ConfigHome, "erun", "config.yaml"),
			[]byte("defaulttenant: elsewhere\n"), 0o644); err != nil {
			t.Fatalf("root cfg: %v", err)
		}
		dockerDir := filepath.Join(setup.Cwd, "team-devops", "docker", "team-devops")
		result := erun.Run(t, []string{"build", "--deploy", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: dockerDir, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for ambiguous tenants, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "build/dry_run_build_deploy_ambiguous_tenants_error", normalize.Apply(result.Combined))
	})

	t.Run("build_deploy_with_project_build_script_errors", func(t *testing.T) {
		// --deploy cannot compose with a project build script: the script
		// owns the whole build and erun cannot know what images it produced.
		// runBuildCommand must fail with a clear error before doing any work.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		if err := os.WriteFile(filepath.Join(setup.Cwd, "build.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write build.sh: %v", err)
		}
		fixture.RunGit(t, setup.Cwd, "add", "build.sh")
		fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add build script")
		result := erun.Run(t, []string{"build", "--deploy", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for --deploy with a build script, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "build/build_deploy_with_project_build_script_errors", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_missing_base_platform_cascades_dependent_rebuild", func(t *testing.T) {
		// Exercises cascadeRebuildsThroughLocalDeps plus the two
		// traceIncrementalDecision arms the all-miss scenarios never reach:
		// the single-platform "missing for platform linux/arm64" reason and
		// the "rebuilding X because dependency Y is rebuilding" cascade. The
		// docker stub is dry-run decision input (the fp-tag inspect answers
		// drive which branch the planner picks; without it the developer's
		// local image cache would shape the golden): the base image's arm64
		// fp-tag is missing (exit 1) so base rebuilds, while every other
		// fp-tag — including both of api's — is present (exit 0). api FROMs
		// the base tag, so despite its own fingerprint hit it must cascade
		// to a rebuild instead of promoting a stale-base image.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		mustWriteFile(t, filepath.Join(setup.Cwd, "erun-devops", "docker", "api", "Dockerfile"),
			"FROM ghcr.io/sophium/base:9.9.9\n")
		fixture.RunGit(t, setup.Cwd, "add", "erun-devops/docker/api/Dockerfile")
		fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "api depends on local base image")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
			`case "$*" in`,
			`  "image inspect ghcr.io/sophium/base:fp-"*"-arm64") exit 1 ;;`,
			`  "image inspect"*) exit 0 ;;`,
			`  *) exit 0 ;;`,
			`esac`,
		}, "\n"))
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
		result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/dry_run_missing_base_platform_cascades_dependent_rebuild", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_versioned_wrapper_resolves_per_arch_base", func(t *testing.T) {
		// Locks the fix for the ${ERUN_VERSION}-wrapper build failure. A wrapper
		// that FROMs its base via ${ERUN_VERSION} resolves the base's unsuffixed
		// local snapshot tag, which is never pushed and (tagged once per arch
		// under one name) only ever holds the last arch built. A multi-platform
		// wrapper build of the other arch therefore can't resolve it on a strict
		// image store. The fix: the base also publishes a per-arch stable tag
		// (…-snapshot-<arch>) and the wrapper's per-platform build asks for the
		// matching arch via ERUN_VERSION=<baseversion>-<arch>. The plan must show
		// both: the per-arch base tag and the per-arch ERUN_VERSION build-arg.
		// --environment local makes versions snapshot-suffixed so BaseVersion is
		// set; the stub answers every fp-tag inspect "missing" so all rebuild.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		mustWriteFile(t, filepath.Join(setup.Cwd, "erun-devops", "docker", "wrapper", "Dockerfile"),
			"FROM ghcr.io/sophium/api:${ERUN_VERSION}\nCMD [\"true\"]\n")
		fixture.RunGit(t, setup.Cwd, "add", "erun-devops/docker/wrapper/Dockerfile")
		fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add ${ERUN_VERSION} wrapper over api")
		result := erun.Run(t, []string{"build", "--dry-run", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/dry_run_versioned_wrapper_resolves_per_arch_base", normalize.Apply(result.Combined))
		// Version normalization collapses the arch suffix (…-snapshot-<ts>-amd64
		// and …-snapshot-amd64 both become <VERSION>), so the per-arch behavior
		// this scenario exists to lock is invisible in the golden. Assert it on
		// the raw output: the wrapper resolves its base per platform, and the
		// base publishes the matching per-arch stable tag. BaseVersion carries no
		// timestamp, so these are stable.
		for _, want := range []string{
			"--build-arg ERUN_VERSION=1.4.2-snapshot-amd64", // wrapper's amd64 build resolves the amd64 base
			"--build-arg ERUN_VERSION=1.4.2-snapshot-arm64", // wrapper's arm64 build resolves the arm64 base
			"ghcr.io/sophium/api:1.4.2-snapshot-amd64",       // base publishes per-arch stable tag
			"ghcr.io/sophium/api:1.4.2-snapshot-arm64",
		} {
			if !strings.Contains(result.Combined, want) {
				t.Errorf("expected per-arch base resolution %q in output:\n%s", want, result.Combined)
			}
		}
	})

	t.Run("real_run_versioned_wrapper_tags_per_arch_base", func(t *testing.T) {
		// Companion to the dry-run scenario: drives runMultiPlatformBuild and the
		// per-arch tagStableBaseVersionAfterBuild for real. The docker stub
		// returns exit 1 for `image inspect` (no fp images → everything rebuilds)
		// and exit 0 otherwise, so the per-arch base re-tag (docker tag) and the
		// wrapper build run against the stub rather than a real daemon.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		mustWriteFile(t, filepath.Join(setup.Cwd, "erun-devops", "docker", "wrapper", "Dockerfile"),
			"FROM ghcr.io/sophium/api:${ERUN_VERSION}\nCMD [\"true\"]\n")
		fixture.RunGit(t, setup.Cwd, "add", "erun-devops/docker/wrapper/Dockerfile")
		fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add ${ERUN_VERSION} wrapper over api")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
			`case "$1 $2" in`,
			`  "image inspect") exit 1 ;;`,
			`  *) exit 0 ;;`,
			`esac`,
		}, "\n"))
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
		result := erun.Run(t, []string{"build", "-v", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/real_run_versioned_wrapper_tags_per_arch_base", normalize.Apply(result.Combined))
	})

	t.Run("real_run_release_push_auth_failure_retries_after_gh_login", func(t *testing.T) {
		// Exercises the build-side GHCR auth-retry chain:
		// runDockerBuildWithRetry catches the DockerRegistryAuthError that
		// runDockerPushOnce raises when the release push is denied,
		// promptDockerLoginRetry auto-confirms via ERUN_AUTO_LOGIN_ON_PUSH,
		// DockerRegistryLogin takes its GHCR branch, and tryGHCRLoginViaGH
		// resolves user + token from the gh stub and re-auths docker before
		// the whole image build retries and the pushes succeed. The auth
		// message deliberately matches neither create_package nor
		// scope-denied so handleNamespaceAuthError falls through to the
		// login-and-retry prompt path.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		stubs := setup.Cwd + "/stubs"
		counter := filepath.Join(stubs, "docker-push-counter")
		fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
			`case "$1" in`,
			`  push)`,
			`    count=0`,
			`    if [ -f '` + counter + `' ]; then count=$(cat '` + counter + `'); fi`,
			`    count=$((count + 1))`,
			`    printf '%s' "$count" > '` + counter + `'`,
			`    if [ "$count" = "1" ]; then`,
			`      printf 'unauthorized: authentication required: denied: requested access to the resource is denied\n' >&2`,
			`      exit 1`,
			`    fi`,
			`    exit 0 ;;`,
			`  image)`,
			`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
			`  *) exit 0 ;;`,
			`esac`,
		}, "\n"))
		// gh answers the user lookup and token read tryGHCRLoginViaGH
		// performs; docker's `login --password-stdin` lands in the stub's
		// default exit-0 arm.
		fixture.StubBinaryWithScript(t, stubs, "gh", strings.Join([]string{
			`case "$1 $2" in`,
			`  "api user") printf 'octo-owner\n'; exit 0 ;;`,
			`  "auth token") printf 'gh-token\n'; exit 0 ;;`,
			`  *) exit 0 ;;`,
			`esac`,
		}, "\n"))
		// Release operations (tag, push) go through the git stub so the
		// release stage succeeds without a real remote.
		fixture.StubBinary(t, stubs, "git", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "gh", "git")...)
		// tryGHCRLoginViaGH gates on exec.LookPath("gh"), which reads PATH
		// rather than the ERUN_<NAME>_BIN override.
		envVars = append(envVars, "PATH="+stubs+":"+os.Getenv("PATH"))
		envVars = append(envVars, "ERUN_AUTO_LOGIN_ON_PUSH=1")
		// -vv so the `docker login ghcr.io` TraceCommand that gates the
		// retry is locked in the golden; at lower verbosity the retry is
		// only provable by the exit code.
		result := erun.Run(t, []string{"build", "--release", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/real_run_release_push_auth_failure_retries_after_gh_login", normalize.Apply(result.Combined))
		// The push counter is a side effect outside the captured streams:
		// >= 2 proves the failed push really was retried after the login
		// rather than the auth error being swallowed.
		rawCount, err := os.ReadFile(counter)
		if err != nil {
			t.Fatalf("read push counter: %v", err)
		}
		if pushes, convErr := strconv.Atoi(strings.TrimSpace(string(rawCount))); convErr != nil || pushes < 2 {
			t.Fatalf("expected at least 2 docker push invocations (fail + retry), got %q", rawCount)
		}
	})

	t.Run("dry_run_release_pushes_release_tagged_docker_builds", func(t *testing.T) {
		// Exercises build.go --release path: per-platform docker build +
		// docker push trace must appear in the dry-run output for the
		// release-tagged image, plus the local tag for downstream
		// dependencies.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		result := erun.Run(t, []string{"build", "--release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/dry_run_release_pushes_release_tagged_docker_builds", normalize.Apply(result.Combined))
	})
}

// stubDockerNoLocalImages routes docker through a stub that fails every
// invocation with exit code 1 — the classification DockerImageExists and
// DockerManifestExists give a real "No such image" miss. Dry-run build and
// deploy scenarios consult docker as decision input (fingerprint inspects,
// manifest probes) even though they mutate nothing; without the stub those
// scenarios silently depend on a docker CLI being installed on the host and
// fail in docker-less environments such as the image build's test stage.
// Returns the ERUN_DOCKER_BIN env pair to append to the scenario's env.
func stubDockerNoLocalImages(t *testing.T, setup env.Setup) []string {
	t.Helper()
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryAdvanced(t, stubs, "docker", fixture.StubBinarySpec{ExitCode: 1})
	return fixture.StubEnv(stubs, "docker")
}
