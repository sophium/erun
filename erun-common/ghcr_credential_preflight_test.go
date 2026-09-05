package eruncommon

import (
	"strings"
	"testing"
)

// The failure this exists to prevent: a brand-new environment that has never
// authenticated to ghcr.io discovers that only after a full multi-arch build
// spends itself at the push (#1201). It must be caught before the build, with
// a message distinguishable from a bad-scope or create-package denial.
func TestVerifyGHCRCredentialConfiguredRefusesWhenNothingResolves(t *testing.T) {
	useDockerConfigDir(t, t.TempDir())
	useGHToken(t, func(string) (string, bool) { return "", false })
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	err := VerifyGHCRCredentialConfigured("ghcr.io/sophium/frs-app:1.0.0")
	if err == nil {
		t.Fatal("expected a refusal when no credential source resolves")
	}
	missing, ok := err.(*MissingGHCRCredentialError)
	if !ok {
		t.Fatalf("expected *MissingGHCRCredentialError, got %T: %v", err, err)
	}
	message := missing.Error()
	for _, want := range []string{"ghcr.io", "gh auth login", "docker login", "erun release"} {
		if !strings.Contains(message, want) {
			t.Fatalf("the error must tell the operator how to fix it, missing %q in:\n%s", want, message)
		}
	}
}

func TestVerifyGHCRCredentialConfiguredPassesViaDockerConfig(t *testing.T) {
	dir := writeDockerConfig(t, `{"auths":{"ghcr.io":{"auth":"YWxpY2U6czNjcmV0"}}}`)
	useDockerConfigDir(t, dir)
	useGHToken(t, func(string) (string, bool) { return "", false })
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	if err := VerifyGHCRCredentialConfigured("ghcr.io/sophium/frs-app:1.0.0"); err != nil {
		t.Fatalf("a docker config credential must be enough, got %v", err)
	}
}

func TestVerifyGHCRCredentialConfiguredPassesViaGHSession(t *testing.T) {
	useDockerConfigDir(t, t.TempDir())
	useGHToken(t, func(string) (string, bool) { return "gho_token", true })
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	if err := VerifyGHCRCredentialConfigured("ghcr.io/sophium/frs-app:1.0.0"); err != nil {
		t.Fatalf("a gh session must be enough, got %v", err)
	}
}

func TestVerifyGHCRCredentialConfiguredPassesViaEnvToken(t *testing.T) {
	useDockerConfigDir(t, t.TempDir())
	useGHToken(t, func(string) (string, bool) { return "", false })
	t.Setenv("GH_TOKEN", "env-token")
	t.Setenv("GITHUB_TOKEN", "")

	if err := VerifyGHCRCredentialConfigured("ghcr.io/sophium/frs-app:1.0.0"); err != nil {
		t.Fatalf("GH_TOKEN must be enough, got %v", err)
	}
}

// Only ghcr is checked; another registry's credential story is a separate
// concern this preflight family does not police.
func TestVerifyGHCRCredentialConfiguredSkipsNonGHCRRegistries(t *testing.T) {
	useDockerConfigDir(t, t.TempDir())
	useGHToken(t, func(string) (string, bool) { return "", false })
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	if err := VerifyGHCRCredentialConfigured("020362606330.dkr.ecr.eu-west-2.amazonaws.com/acme/api:1.0.0"); err != nil {
		t.Fatalf("a non-ghcr registry must not be judged by ghcr credential resolution, got %v", err)
	}
}

func TestVerifyGHCRChartCredentialConfiguredRefusesWhenNothingResolves(t *testing.T) {
	useDockerConfigDir(t, t.TempDir())
	useGHToken(t, func(string) (string, bool) { return "", false })
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	err := VerifyGHCRChartCredentialConfigured("oci://ghcr.io/sophium/charts")
	if err == nil {
		t.Fatal("expected a refusal when no credential source resolves")
	}
	if _, ok := err.(*MissingGHCRCredentialError); !ok {
		t.Fatalf("expected *MissingGHCRCredentialError, got %T: %v", err, err)
	}
}

func TestVerifyGHCRChartCredentialConfiguredPasses(t *testing.T) {
	dir := writeDockerConfig(t, `{"auths":{"ghcr.io":{"auth":"YWxpY2U6czNjcmV0"}}}`)
	useDockerConfigDir(t, dir)
	useGHToken(t, func(string) (string, bool) { return "", false })
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")

	if err := VerifyGHCRChartCredentialConfigured("oci://ghcr.io/sophium/charts"); err != nil {
		t.Fatalf("a docker config credential must be enough, got %v", err)
	}
}
