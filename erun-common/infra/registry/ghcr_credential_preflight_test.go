package registry

import (
	"errors"
	"testing"
)

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
	var missing *MissingGHCRCredentialError
	if !errors.As(err, &missing) {
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
