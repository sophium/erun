package registry

import (
	"errors"
	"testing"
)

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
