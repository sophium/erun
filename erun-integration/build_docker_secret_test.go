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
	// Only the secret reference reaches the argv, so the trace is safe to log.
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
	// "code1" has no entry and must inherit the project default.
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
	// The default's src does not exist: the opt-out must win before it is read.
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
	// Dockerfiles guard on `[ -f /run/secrets/<id> ]`, so a missing secret
	// would silently skip work and still exit zero.
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
	// docker parses --secret as comma-separated pairs.
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
