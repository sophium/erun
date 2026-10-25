package runtimeversion

import (
	"os"
	"path/filepath"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud/providertoken"
)

func testPlatformAliasProvider() model.CloudProviderConfig {
	return model.CloudProviderConfig{
		Alias:         "erun+api.erunpaas.com@erun",
		Provider:      configpkg.CloudProviderERun,
		Username:      "erun",
		AccountID:     "api.erunpaas.com",
		OIDCIssuerURL: "https://api.erunpaas.com",
		ERun: &model.ERunProviderConfig{
			APIURL:          providertoken.HostedPlatformAPIURL,
			ClientID:        "erun-cli",
			RefreshTokenRef: providertoken.ErunRefreshTokenRef("erun+api.erunpaas.com@erun"),
		},
	}
}

// installCapturingKubectl points ERUN_KUBECTL_BIN at a stub that records its stdin.
func installCapturingKubectl(t *testing.T) *capturedManifest {
	t.Helper()
	dir := t.TempDir()
	capture := filepath.Join(dir, "stdin.yaml")
	script := "#!/bin/sh\ncat > " + capture + "\n"
	path := filepath.Join(dir, "kubectl-capture")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("ERUN_KUBECTL_BIN", path)
	return &capturedManifest{path: capture}
}

type capturedManifest struct {
	path string
}
