package integration

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func buildDockerSecretScenarios(t *testing.T) {
	t.Run("dry_run_configured_docker_secret_reaches_build_argv", testBuildDryRunConfiguredDockerSecretReachesBuildArgv)
	t.Run("dry_run_project_docker_secret_default_applies_to_unlisted_environment", testBuildDryRunProjectDockerSecretDefaultAppliesToUnlistedEnvironment)
	t.Run("dry_run_blank_docker_secret_entry_is_dropped", testBuildDryRunBlankDockerSecretEntryIsDropped)
	t.Run("dry_run_empty_docker_secret_list_opts_environment_out", testBuildDryRunEmptyDockerSecretListOptsEnvironmentOut)
	t.Run("fails_when_declared_docker_secret_environment_variable_is_unset", testBuildFailsWhenDeclaredDockerSecretEnvironmentVariableIsUnset)
	t.Run("fails_when_declared_docker_secret_file_cannot_be_read", testBuildFailsWhenDeclaredDockerSecretFileCannotBeRead)
	t.Run("fails_when_docker_secret_entry_has_no_id", testBuildFailsWhenDockerSecretEntryHasNoId)
	t.Run("fails_when_docker_secret_entry_declares_neither_env_nor_src", testBuildFailsWhenDockerSecretEntryDeclaresNeitherEnvNorSrc)
	t.Run("fails_when_docker_secret_entry_declares_both_env_and_src", testBuildFailsWhenDockerSecretEntryDeclaresBothEnvAndSrc)
	t.Run("fails_when_docker_secret_field_contains_a_comma", testBuildFailsWhenDockerSecretFieldContainsAComma)
}

func testBuildDryRunConfiguredDockerSecretReachesBuildArgv(t *testing.T) {
	// environments.<env>.docker.secrets declares a BuildKit build secret for
	// this env. The credential stays in the environment variable docker reads
	// it from; what reaches the build argv is the reference only, so the
	// trace and the command are safe to log.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	fixture.SeedProjectK8sConfig(t, setup,
		"environments:\n"+
			"  local:\n"+
			"    docker:\n"+
			"      secrets:\n"+
			"        - id: ghcr\n"+
			"          env: ERUN_TEST_CHART_TOKEN\n",
	)
	envVars := append(setup.Env(), stubDockerNoLocalImages(t, setup)...)
	envVars = append(envVars, "ERUN_TEST_CHART_TOKEN="+dockerSecretFixtureValue)
	result := erun.Run(t, []string{"build", "--dry-run", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, dockerSecretFixtureValue) {
		t.Fatalf("the secret value must never reach the build's output, only the reference to it:\n%s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_configured_docker_secret_reaches_build_argv", normalize.Apply(result.Combined))
}

func testBuildDryRunProjectDockerSecretDefaultAppliesToUnlistedEnvironment(t *testing.T) {
	// docker.secrets at the top level is the project default, inherited by
	// every environment that declares no secrets of its own. A project that
	// always needs one private registry credential states it once, so an
	// environment nobody listed (here "code1", which has no entry at all)
	// still receives it rather than silently building without it.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	src := filepath.Join(setup.Cwd, "build-secrets", "chart-repo.json")
	mustWriteFile(t, src, "{}\n")
	fixture.SeedProjectK8sConfig(t, setup,
		"docker:\n"+
			"  secrets:\n"+
			"    - id: acme-charts\n"+
			"      src: "+src+"\n",
	)
	result := erun.Run(t, []string{"build", "--dry-run", "--environment", "code1"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_project_docker_secret_default_applies_to_unlisted_environment", normalize.Apply(result.Combined))
}

func testBuildDryRunBlankDockerSecretEntryIsDropped(t *testing.T) {
	// A list literal can leave a wholly blank entry behind. It declares
	// nothing, so it must not reach the build as an empty --secret, and it
	// must not disturb the entry beside it.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	fixture.SeedProjectK8sConfig(t, setup,
		"environments:\n"+
			"  local:\n"+
			"    docker:\n"+
			"      secrets:\n"+
			"        -\n"+
			"        - id: ghcr\n"+
			"          env: ERUN_TEST_CHART_TOKEN\n",
	)
	envVars := append(setup.Env(), stubDockerNoLocalImages(t, setup)...)
	envVars = append(envVars, "ERUN_TEST_CHART_TOKEN="+dockerSecretFixtureValue)
	result := erun.Run(t, []string{"build", "--dry-run", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/dry_run_blank_docker_secret_entry_is_dropped", normalize.Apply(result.Combined))
}

func testBuildDryRunEmptyDockerSecretListOptsEnvironmentOut(t *testing.T) {
	// The explicit opt-out: an environment declaring secrets: [] inherits
	// nothing from the project default, so it builds with no secrets even
	// though the default names one. The project default's src deliberately
	// does not exist here — the opt-out must win before the default is read,
	// not after it has already been validated.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	missing := filepath.Join(setup.Cwd, "build-secrets", "absent.json")
	fixture.SeedProjectK8sConfig(t, setup,
		"docker:\n"+
			"  secrets:\n"+
			"    - id: acme-charts\n"+
			"      src: "+missing+"\n"+
			"environments:\n"+
			"  local:\n"+
			"    docker:\n"+
			"      secrets: []\n",
	)
	result := erun.Run(t, []string{"build", "--dry-run", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "--secret") {
		t.Fatalf("expected an explicit secrets: [] to opt this environment out of the project default:\n%s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_empty_docker_secret_list_opts_environment_out", normalize.Apply(result.Combined))
}

func testBuildFailsWhenDeclaredDockerSecretEnvironmentVariableIsUnset(t *testing.T) {
	// A declared secret that cannot be supplied must fail the build. A
	// Dockerfile guards secret-dependent work with `if [ -f
	// /run/secrets/<id> ]`, so a secret that silently fails to arrive makes
	// the build skip that work and still exit zero — reporting success having
	// verified less than the project asked for. The error names the missing
	// variable and the remedy.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	fixture.SeedProjectK8sConfig(t, setup,
		"environments:\n"+
			"  local:\n"+
			"    docker:\n"+
			"      secrets:\n"+
			"        - id: ghcr\n"+
			"          env: ERUN_TEST_UNSET_CHART_TOKEN\n",
	)
	result := erun.Run(t, []string{"build", "--dry-run", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode == 0 {
		t.Fatalf("expected the build to fail when a declared secret's environment variable is unset; got exit 0:\n%s", result.Combined)
	}
	golden.Equal(t, "build/fails_when_declared_docker_secret_environment_variable_is_unset", normalize.Apply(result.Combined))
}

func testBuildFailsWhenDeclaredDockerSecretFileCannotBeRead(t *testing.T) {
	// The src form gets the same treatment: a path that cannot be read fails
	// the build and names the remedy, rather than handing docker a secret
	// source that is not there.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	missing := filepath.Join(setup.Cwd, "build-secrets", "absent.json")
	fixture.SeedProjectK8sConfig(t, setup,
		"environments:\n"+
			"  local:\n"+
			"    docker:\n"+
			"      secrets:\n"+
			"        - id: acme-charts\n"+
			"          src: "+missing+"\n",
	)
	result := erun.Run(t, []string{"build", "--dry-run", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode == 0 {
		t.Fatalf("expected the build to fail when a declared secret's file cannot be read; got exit 0:\n%s", result.Combined)
	}
	golden.Equal(t, "build/fails_when_declared_docker_secret_file_cannot_be_read", normalize.Apply(result.Combined))
}

func testBuildFailsWhenDockerSecretEntryHasNoId(t *testing.T) {
	// id is what the Dockerfile mounts by, so an entry without one cannot be
	// mounted at all. Refusing it beats passing docker a secret the
	// Dockerfile's --mount=type=secret can never name.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	fixture.SeedProjectK8sConfig(t, setup,
		"environments:\n"+
			"  local:\n"+
			"    docker:\n"+
			"      secrets:\n"+
			"        - env: ERUN_TEST_CHART_TOKEN\n",
	)
	envVars := append(setup.Env(), stubDockerNoLocalImages(t, setup)...)
	envVars = append(envVars, "ERUN_TEST_CHART_TOKEN="+dockerSecretFixtureValue)
	result := erun.Run(t, []string{"build", "--dry-run", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected the build to fail for a docker.secrets entry with no id; got exit 0:\n%s", result.Combined)
	}
	golden.Equal(t, "build/fails_when_docker_secret_entry_has_no_id", normalize.Apply(result.Combined))
}

func testBuildFailsWhenDockerSecretEntryDeclaresNeitherEnvNorSrc(t *testing.T) {
	// The other half of exactly-one-source: an entry naming an id but no
	// source at all has nothing to mount, and passing it through would leave
	// the Dockerfile's mount empty.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	fixture.SeedProjectK8sConfig(t, setup,
		"environments:\n"+
			"  local:\n"+
			"    docker:\n"+
			"      secrets:\n"+
			"        - id: ghcr\n",
	)
	result := erun.Run(t, []string{"build", "--dry-run", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode == 0 {
		t.Fatalf("expected the build to fail for a docker.secrets entry declaring neither env nor src; got exit 0:\n%s", result.Combined)
	}
	golden.Equal(t, "build/fails_when_docker_secret_entry_declares_neither_env_nor_src", normalize.Apply(result.Combined))
}

func testBuildFailsWhenDockerSecretEntryDeclaresBothEnvAndSrc(t *testing.T) {
	// Exactly one source per entry. Accepting both would silently pick one
	// and mount a credential the project did not intend.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	src := filepath.Join(setup.Cwd, "build-secrets", "chart-repo.json")
	mustWriteFile(t, src, "{}\n")
	fixture.SeedProjectK8sConfig(t, setup,
		"environments:\n"+
			"  local:\n"+
			"    docker:\n"+
			"      secrets:\n"+
			"        - id: ghcr\n"+
			"          env: ERUN_TEST_CHART_TOKEN\n"+
			"          src: "+src+"\n",
	)
	envVars := append(setup.Env(), stubDockerNoLocalImages(t, setup)...)
	envVars = append(envVars, "ERUN_TEST_CHART_TOKEN="+dockerSecretFixtureValue)
	result := erun.Run(t, []string{"build", "--dry-run", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected the build to fail for a docker.secrets entry declaring both env and src; got exit 0:\n%s", result.Combined)
	}
	golden.Equal(t, "build/fails_when_docker_secret_entry_declares_both_env_and_src", normalize.Apply(result.Combined))
}

func testBuildFailsWhenDockerSecretFieldContainsAComma(t *testing.T) {
	// docker parses the --secret value as a comma-separated key=value list,
	// so a comma inside a field would be read as the start of another pair
	// and silently change which secret gets mounted.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	fixture.SeedProjectK8sConfig(t, setup,
		"environments:\n"+
			"  local:\n"+
			"    docker:\n"+
			"      secrets:\n"+
			"        - id: ghcr\n"+
			"          env: \"ERUN_TEST_TOKEN,name=other\"\n",
	)
	result := erun.Run(t, []string{"build", "--dry-run", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode == 0 {
		t.Fatalf("expected the build to fail for a docker.secrets field containing a comma; got exit 0:\n%s", result.Combined)
	}
	golden.Equal(t, "build/fails_when_docker_secret_field_contains_a_comma", normalize.Apply(result.Combined))
}
