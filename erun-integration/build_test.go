package integration

import (
	"os"
	"path/filepath"
	"regexp"
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
		result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
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
		result := erun.Run(t, []string{"build", "--release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/dry_run_release_includes_release_and_build_traces", normalize.Apply(result.Combined))
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
		result := erun.Run(t, []string{"build", "--dry-run", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
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
		result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
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
			result := erun.Run(t, []string{"build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
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

	t.Run("dry_run_release_pushes_release_tagged_docker_builds", func(t *testing.T) {
		// Exercises build.go --release path: per-platform docker build +
		// docker push trace must appear in the dry-run output for the
		// release-tagged image, plus the local tag for downstream
		// dependencies.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		result := erun.Run(t, []string{"build", "--release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "build/dry_run_release_pushes_release_tagged_docker_builds", normalize.Apply(result.Combined))
	})
}
