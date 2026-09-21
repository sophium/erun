package eruncommon

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// loadProjectConfigForDockerSecrets reads the project config a build resolves
// its `docker.secrets` from. It reports ok=false for a project that has no
// `.erun/config.yaml` at all, which is not an error: such a project declares no
// secrets, and its docker build command stays byte-for-byte what it was before
// this key existed.
func loadProjectConfigForDockerSecrets(projectRoot string) (ProjectConfig, bool, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return ProjectConfig{}, false, nil
	}

	cfg, _, err := LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
			return ProjectConfig{}, false, nil
		}
		return ProjectConfig{}, false, err
	}
	return cfg, true, nil
}

// normalizeDockerBuildSecrets trims the configured entries, drops the wholly
// blank ones a YAML list literal can leave behind, and refuses anything that
// would not mean what it says.
//
// Validation is a trust-boundary concern: the config file is written by hand,
// and a malformed entry that is passed through silently is how a build ends up
// mounting nothing while reporting success. Each refusal names the remedy.
func normalizeDockerBuildSecrets(secrets []DockerBuildSecret) ([]DockerBuildSecret, error) {
	out := make([]DockerBuildSecret, 0, len(secrets))
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

// normalizeDockerBuildSecret trims one configured entry and reports whether it
// declares anything at all, refusing one that would not mean what it says.
func normalizeDockerBuildSecret(index int, secret DockerBuildSecret) (DockerBuildSecret, bool, error) {
	secret.ID = strings.TrimSpace(secret.ID)
	secret.Env = strings.TrimSpace(secret.Env)
	secret.Src = strings.TrimSpace(secret.Src)

	if secret.ID == "" && secret.Env == "" && secret.Src == "" {
		return DockerBuildSecret{}, false, nil
	}
	if secret.ID == "" {
		return DockerBuildSecret{}, false, fmt.Errorf("docker.secrets entry %d: id is required, naming the secret the Dockerfile mounts with --mount=type=secret,id=<id>", index+1)
	}
	if secret.Env == "" && secret.Src == "" {
		return DockerBuildSecret{}, false, fmt.Errorf("docker.secrets entry %q: declare env (the environment variable holding the credential) or src (the file holding it)", secret.ID)
	}
	if secret.Env != "" && secret.Src != "" {
		return DockerBuildSecret{}, false, fmt.Errorf("docker.secrets entry %q: declare env or src, not both", secret.ID)
	}
	if err := validateDockerSecretFields(secret); err != nil {
		return DockerBuildSecret{}, false, err
	}
	return secret, true, nil
}

// validateDockerSecretFields refuses a field value docker would misread. docker
// parses the --secret value as a comma-separated key=value list, so a comma
// inside a field would be read as the start of another pair and silently change
// which secret gets mounted.
func validateDockerSecretFields(secret DockerBuildSecret) error {
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

// describeDockerBuildSecrets renders a resolved secret list for a trace line.
// Only references appear here — an id and the variable or path it reads from —
// so the rendered form is safe to log.
func describeDockerBuildSecrets(secrets []DockerBuildSecret) string {
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

// applyDockerSecrets resolves this build's declared build secrets onto the
// build spec, and fails the build when one of them cannot actually be supplied.
//
// Failing is the point, and it is the reason this is an error rather than a
// warning. A declared secret that is absent at build time does not make
// BuildKit mount nothing loudly: the Dockerfile's own `if [ -f
// /run/secrets/<id> ]` guard — or a mount left at its default
// `required=false` — degrades to skipping precisely the work the secret exists
// to enable. The build then reports success having verified less than the
// project asked it to, which is the one outcome a gate must never have. An
// error here names the config key, the id, and the missing variable or path
// instead, and the remedy is a one-line config or environment change.
func applyDockerSecrets(ctx Context, projectRoot, environment string, build *DockerBuildSpec) error {
	cfg, ok, err := loadProjectConfigForDockerSecrets(projectRoot)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}

	secrets, err := normalizeDockerBuildSecrets(cfg.DockerSecretsForEnvironment(environment))
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
	ctx.Trace("build: secrets configured as " + describeDockerBuildSecrets(secrets) +
		" (.erun/config.yaml " + cfg.DockerSecretsOrigin(environment) + ")")
	return nil
}

// validateDockerSecretsAvailable refuses a declared secret that cannot be
// supplied, naming what is missing and the one-line remedy.
func validateDockerSecretsAvailable(secrets []DockerBuildSecret) error {
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

// dockerSecretArgs renders each declared build secret as the
// `--secret <reference>` pair BuildKit expects, for the docker build argv.
// Every reference names where the credential comes from — an environment
// variable, or a path — never what it is, so nothing secret is ever placed on
// the command line.
func dockerSecretArgs(secrets []DockerBuildSecret) []string {
	args := make([]string, 0, len(secrets)*2)
	for _, secret := range secrets {
		args = append(args, "--secret", dockerSecretArg(secret))
	}
	return args
}

// dockerSecretArg renders one declared build secret as the value BuildKit's
// --secret expects: `id=<id>,env=<VAR>` or `id=<id>,src=<path>`. Both forms
// name where the credential comes from, never what it is.
func dockerSecretArg(secret DockerBuildSecret) string {
	if secret.Env != "" {
		return "id=" + secret.ID + ",env=" + secret.Env
	}
	return "id=" + secret.ID + ",src=" + secret.Src
}
