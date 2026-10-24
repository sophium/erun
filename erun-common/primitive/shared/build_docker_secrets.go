package shared

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// LoadProjectConfigForDockerSecrets reports ok=false, not an error, without a config.
func LoadProjectConfigForDockerSecrets(projectRoot string) (config.ProjectConfig, bool, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return config.ProjectConfig{}, false, nil
	}

	cfg, _, err := config.LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, config.ErrNotInitialized) {
			return config.ProjectConfig{}, false, nil
		}
		return config.ProjectConfig{}, false, err
	}
	return cfg, true, nil
}

// NormalizeDockerBuildSecrets refuses malformed entries, which would otherwise
// mount nothing while the build reports success.
func NormalizeDockerBuildSecrets(secrets []config.DockerBuildSecret) ([]config.DockerBuildSecret, error) {
	out := make([]config.DockerBuildSecret, 0, len(secrets))
	for i, secret := range secrets {
		normalized, declared, err := normalizeDockerBuildSecret(i, secret)
		if err != nil {
			return nil, err
		}
		if declared {
			out = append(out, normalized)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func normalizeDockerBuildSecret(index int, secret config.DockerBuildSecret) (config.DockerBuildSecret, bool, error) {
	secret.ID = strings.TrimSpace(secret.ID)
	secret.Env = strings.TrimSpace(secret.Env)
	secret.Src = strings.TrimSpace(secret.Src)

	if secret.ID == "" && secret.Env == "" && secret.Src == "" {
		return config.DockerBuildSecret{}, false, nil
	}
	if secret.ID == "" {
		return config.DockerBuildSecret{}, false, fmt.Errorf("docker.secrets entry %d: id is required, naming the secret the Dockerfile mounts with --mount=type=secret,id=<id>", index+1)
	}
	if secret.Env == "" && secret.Src == "" {
		return config.DockerBuildSecret{}, false, fmt.Errorf("docker.secrets entry %q: declare env (the environment variable holding the credential) or src (the file holding it)", secret.ID)
	}
	if secret.Env != "" && secret.Src != "" {
		return config.DockerBuildSecret{}, false, fmt.Errorf("docker.secrets entry %q: declare env or src, not both", secret.ID)
	}
	if err := validateDockerSecretFields(secret); err != nil {
		return config.DockerBuildSecret{}, false, err
	}
	return secret, true, nil
}

// validateDockerSecretFields refuses commas, which docker reads as a new pair.
func validateDockerSecretFields(secret config.DockerBuildSecret) error {
	for _, field := range []struct{ name, value string }{
		{"id", secret.ID},
		{"env", secret.Env},
		{"src", secret.Src},
	} {
		if strings.Contains(field.value, ",") {
			return fmt.Errorf("docker.secrets entry %q: %s must not contain a comma, which docker reads as a key=value separator in --secret", secret.ID, field.name)
		}
	}
	return nil
}

// DescribeDockerBuildSecrets renders only references, so it is safe to log.
func DescribeDockerBuildSecrets(secrets []config.DockerBuildSecret) string {
	parts := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if secret.Env != "" {
			parts = append(parts, secret.ID+" (env "+secret.Env+")")
			continue
		}
		parts = append(parts, secret.ID+" (src "+secret.Src+")")
	}
	return strings.Join(parts, ", ")
}

// ApplyDockerSecrets fails on a missing secret: a Dockerfile's `-f` guard
// would otherwise silently skip the work and the build would report success.
func ApplyDockerSecrets(ctx core.Context, projectRoot, environment string, build *DockerBuildSpec) error {
	cfg, ok, err := LoadProjectConfigForDockerSecrets(projectRoot)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	secrets, err := NormalizeDockerBuildSecrets(cfg.DockerSecretsForEnvironment(environment))
	if err != nil {
		return err
	}
	if len(secrets) == 0 {
		return nil
	}
	if err := validateDockerSecretsAvailable(secrets); err != nil {
		return err
	}

	build.DockerSecrets = secrets
	ctx.Trace("build: secrets configured as " + DescribeDockerBuildSecrets(secrets) +
		" (.erun/config.yaml " + cfg.DockerSecretsOrigin(environment) + ")")
	return nil
}

func validateDockerSecretsAvailable(secrets []config.DockerBuildSecret) error {
	for _, secret := range secrets {
		if secret.Env != "" {
			if strings.TrimSpace(os.Getenv(secret.Env)) == "" {
				return fmt.Errorf("docker.secrets entry %q needs the environment variable %s, which is not set: set it, or point the entry at a file with `src:`", secret.ID, secret.Env)
			}
			continue
		}
		if _, statErr := os.Stat(secret.Src); statErr != nil {
			return fmt.Errorf("docker.secrets entry %q needs %s, which cannot be read: fix the path, or point the entry at an environment variable with `env:`", secret.ID, secret.Src)
		}
	}
	return nil
}

// DockerSecretArgs names where each credential comes from, never its value.
func DockerSecretArgs(secrets []config.DockerBuildSecret) []string {
	args := make([]string, 0, len(secrets)*2)
	for _, secret := range secrets {
		args = append(args, "--secret", dockerSecretArg(secret))
	}
	return args
}

func dockerSecretArg(secret config.DockerBuildSecret) string {
	if secret.Env != "" {
		return "id=" + secret.ID + ",env=" + secret.Env
	}
	return "id=" + secret.ID + ",src=" + secret.Src
}
