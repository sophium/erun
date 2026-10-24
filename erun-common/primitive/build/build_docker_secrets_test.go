package build

import (
	"slices"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

func TestNormalizeDockerBuildSecretsRefusesEntriesThatWouldNotMeanWhatTheySay(t *testing.T) {
	cases := []struct {
		name    string
		secrets []configpkg.DockerBuildSecret
		want    []configpkg.DockerBuildSecret
		wantErr string
	}{
		{
			name:    "an id containing the key=value separator",
			secrets: []configpkg.DockerBuildSecret{{ID: "ghcr,env=OTHER", Env: "ACME_TOKEN"}},
			wantErr: "comma",
		},
		{
			name:    "a src containing the key=value separator",
			secrets: []configpkg.DockerBuildSecret{{ID: "ghcr", Src: "/tmp/a,b"}},
			wantErr: "comma",
		},
		{
			name:    "whitespace is trimmed rather than passed to docker",
			secrets: []configpkg.DockerBuildSecret{{ID: " ghcr ", Env: " ACME_TOKEN "}},
			want:    []configpkg.DockerBuildSecret{{ID: "ghcr", Env: "ACME_TOKEN"}},
		},
		{
			name:    "nothing declared stays nil",
			secrets: nil,
			want:    nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := dockerspec.NormalizeDockerBuildSecrets(tc.secrets)
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

	cfg, _, err := configpkg.LoadProjectConfig(projectRoot)
	if err != nil {
		t.Fatalf("LoadProjectConfig(%q) failed: %v", projectRoot, err)
	}

	cases := []struct {
		name        string
		environment string
		want        []configpkg.DockerBuildSecret
		wantOrigin  string
	}{
		{
			name:        "an environment's own list wins over the project default",
			environment: "overridden",
			want:        []configpkg.DockerBuildSecret{{ID: "env-own", Env: "ACME_ENV_TOKEN"}},
			wantOrigin:  "environments.overridden.docker.secrets",
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
