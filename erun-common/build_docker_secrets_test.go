package eruncommon

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// dockerBuildSpecForSecretTest is a build spec with the fields dockerBuildArgs
// needs and nothing else, so each case below turns on one declared secret.
func dockerBuildSpecForSecretTest() DockerBuildSpec {
	return DockerBuildSpec{
		ContextDir:     ".",
		DockerfilePath: "Dockerfile",
		Image:          DockerImageReference{Tag: "ghcr.io/acme/app:1.0.0"},
		Platforms:      []string{"linux/amd64"},
	}
}

// secretArgvValue returns the value passed to --secret in an assembled docker
// build argv, or "" when none was passed.
func secretArgvValue(t *testing.T, args []string) string {
	t.Helper()
	for i, arg := range args {
		if arg == "--secret" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// TestDockerBuildArgsPassDeclaredSecretsAsReferences is the reproduction of the
// reported failure: a project declares a build secret in .erun/config.yaml, and
// the docker build argv erun assembles must carry it as a --secret reference.
//
// Before the fix no config key existed and no --secret was ever assembled, so
// the build ran without the credential: a Dockerfile step that needs it either
// failed or — where the Dockerfile guards the work with an `if [ -f
// /run/secrets/<id> ]` test — silently skipped it and let the build report
// success having verified less than the project asked for.
//
// The secret is asserted at the argv, not by grepping a log: the argv is the
// contract docker actually receives, and it is where a value that leaked into
// the command line would show up.
func TestDockerBuildArgsPassDeclaredSecretsAsReferences(t *testing.T) {
	const tokenValue = "ghcr_pat_do_not_leak_me"
	t.Setenv("ACME_GHCR_TOKEN", tokenValue)

	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "docker-config.json")
	if err := os.WriteFile(srcPath, []byte(`{"auths":{}}`), 0o600); err != nil {
		t.Fatalf("writing the src secret fixture failed: %v", err)
	}

	projectRoot := writeDockerPlatformsProjectConfig(t, `docker:
    secrets:
        - id: ghcr
          env: ACME_GHCR_TOKEN
        - id: charts
          src: `+srcPath+`
`)

	build := dockerBuildSpecForSecretTest()
	if err := applyDockerSecrets(testTraceContext(false), projectRoot, "code1", &build); err != nil {
		t.Fatalf("applyDockerSecrets failed: %v", err)
	}

	args := dockerBuildArgs(build, "linux/amd64")

	want := []string{"id=ghcr,env=ACME_GHCR_TOKEN", "id=charts,src=" + srcPath}
	got := make([]string, 0, len(want))
	for i, arg := range args {
		if arg == "--secret" && i+1 < len(args) {
			got = append(got, args[i+1])
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("docker build argv --secret values = %v, want %v (full argv: %v)", got, want, args)
	}

	// The reference is the whole point: the credential's value must not appear
	// anywhere on the command line, in either its own element or embedded in the
	// --secret value.
	for _, arg := range args {
		if strings.Contains(arg, tokenValue) {
			t.Fatalf("assembled argv leaked the secret value in %q (full argv: %v)", arg, args)
		}
	}
}

// TestApplyDockerSecretsLeavesBuildsWithoutDeclaredSecretsUnchanged guards the
// default nearly every project is in: no docker.secrets key anywhere means no
// --secret on the command line, byte-for-byte what erun assembled before this
// key existed.
func TestApplyDockerSecretsLeavesBuildsWithoutDeclaredSecretsUnchanged(t *testing.T) {
	projectRoot := writeDockerPlatformsProjectConfig(t, `docker:
    platforms:
        - linux/amd64
`)

	build := dockerBuildSpecForSecretTest()
	if err := applyDockerSecrets(testTraceContext(false), projectRoot, "code1", &build); err != nil {
		t.Fatalf("applyDockerSecrets failed: %v", err)
	}

	args := dockerBuildArgs(build, "linux/amd64")
	if secretArgvValue(t, args) != "" {
		t.Fatalf("a project declaring no docker.secrets got a --secret in its argv: %v", args)
	}
}

// TestApplyDockerSecretsFailsWhenADeclaredSecretCannotBeSupplied is the
// companion to the reproduction: a declared secret that is absent must fail the
// build rather than proceed. Proceeding is the silent under-coverage the report
// is about — the Dockerfile's own guard turns a missing mount into skipped work,
// and the build still exits zero.
func TestApplyDockerSecretsFailsWhenADeclaredSecretCannotBeSupplied(t *testing.T) {
	cases := []struct {
		name    string
		config  string
		wantErr string
	}{
		{
			name: "a declared env var that is not set",
			config: `docker:
    secrets:
        - id: ghcr
          env: ACME_ABSENT_TOKEN
`,
			wantErr: "ACME_ABSENT_TOKEN",
		},
		{
			name: "a declared src path that does not exist",
			config: `docker:
    secrets:
        - id: charts
          src: /nonexistent/acme/docker-config.json
`,
			wantErr: "/nonexistent/acme/docker-config.json",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ACME_ABSENT_TOKEN", "")
			projectRoot := writeDockerPlatformsProjectConfig(t, tc.config)

			build := dockerBuildSpecForSecretTest()
			err := applyDockerSecrets(testTraceContext(false), projectRoot, "code1", &build)
			if err == nil {
				t.Fatalf("applyDockerSecrets accepted a declared secret that cannot be supplied")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not name what is missing (%q), so the remedy is not actionable", err, tc.wantErr)
			}
			if len(build.DockerSecrets) != 0 {
				t.Fatalf("a failed resolution still populated the spec: %+v", build.DockerSecrets)
			}
		})
	}
}

// TestNormalizeDockerBuildSecretsRefusesEntriesThatWouldNotMeanWhatTheySay
// covers the trust boundary: the config file is hand-written, and an entry that
// is passed through unchecked is how a build ends up mounting nothing.
func TestNormalizeDockerBuildSecretsRefusesEntriesThatWouldNotMeanWhatTheySay(t *testing.T) {
	cases := []struct {
		name    string
		secrets []DockerBuildSecret
		want    []DockerBuildSecret
		wantErr string
	}{
		{
			name:    "an entry with no id",
			secrets: []DockerBuildSecret{{Env: "ACME_TOKEN"}},
			wantErr: "id is required",
		},
		{
			name:    "an entry naming neither env nor src",
			secrets: []DockerBuildSecret{{ID: "ghcr"}},
			wantErr: "declare env",
		},
		{
			name:    "an entry naming both env and src",
			secrets: []DockerBuildSecret{{ID: "ghcr", Env: "ACME_TOKEN", Src: "/tmp/x"}},
			wantErr: "not both",
		},
		{
			name:    "an id containing the key=value separator",
			secrets: []DockerBuildSecret{{ID: "ghcr,env=OTHER", Env: "ACME_TOKEN"}},
			wantErr: "comma",
		},
		{
			name:    "a src containing the key=value separator",
			secrets: []DockerBuildSecret{{ID: "ghcr", Src: "/tmp/a,b"}},
			wantErr: "comma",
		},
		{
			name:    "a wholly blank entry a YAML list literal left behind",
			secrets: []DockerBuildSecret{{}, {ID: "ghcr", Env: "ACME_TOKEN"}},
			want:    []DockerBuildSecret{{ID: "ghcr", Env: "ACME_TOKEN"}},
		},
		{
			name:    "whitespace is trimmed rather than passed to docker",
			secrets: []DockerBuildSecret{{ID: " ghcr ", Env: " ACME_TOKEN "}},
			want:    []DockerBuildSecret{{ID: "ghcr", Env: "ACME_TOKEN"}},
		},
		{
			name:    "nothing declared stays nil",
			secrets: nil,
			want:    nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeDockerBuildSecrets(tc.secrets)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("normalizeDockerBuildSecrets accepted %+v", tc.secrets)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not explain the refusal (%q)", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeDockerBuildSecrets(%+v) failed: %v", tc.secrets, err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("normalizeDockerBuildSecrets(%+v) = %+v, want %+v", tc.secrets, got, tc.want)
			}
		})
	}
}

// TestProjectDockerSecretsInheritance locks the inheritance rule to the one
// docker.platforms already uses, so the two sibling keys under `docker:` do not
// diverge: an unlisted environment inherits the project default, an
// environment's own list wins, and an explicit empty list opts out.
func TestProjectDockerSecretsInheritance(t *testing.T) {
	projectRoot := writeDockerPlatformsProjectConfig(t, `docker:
    secrets:
        - id: project-default
          env: ACME_TOKEN
environments:
    overridden:
        docker:
            secrets:
                - id: env-own
                  env: ACME_ENV_TOKEN
    optedout:
        docker:
            secrets: []
`)

	cfg, _, err := LoadProjectConfig(projectRoot)
	if err != nil {
		t.Fatalf("LoadProjectConfig(%q) failed: %v", projectRoot, err)
	}

	cases := []struct {
		name        string
		environment string
		want        []DockerBuildSecret
		wantOrigin  string
	}{
		{
			name:        "an environment with no entry inherits the project default",
			environment: "code5",
			want:        []DockerBuildSecret{{ID: "project-default", Env: "ACME_TOKEN"}},
			wantOrigin:  "docker.secrets (project default)",
		},
		{
			name:        "an environment's own list wins over the project default",
			environment: "overridden",
			want:        []DockerBuildSecret{{ID: "env-own", Env: "ACME_ENV_TOKEN"}},
			wantOrigin:  "environments.overridden.docker.secrets",
		},
		{
			name:        "an explicit empty list opts the environment out",
			environment: "optedout",
			want:        nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cfg.DockerSecretsForEnvironment(tc.environment)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("DockerSecretsForEnvironment(%q) = %+v, want %+v", tc.environment, got, tc.want)
			}
			if tc.wantOrigin != "" {
				if origin := cfg.DockerSecretsOrigin(tc.environment); origin != tc.wantOrigin {
					t.Fatalf("DockerSecretsOrigin(%q) = %q, want %q", tc.environment, origin, tc.wantOrigin)
				}
			}
		})
	}
}
