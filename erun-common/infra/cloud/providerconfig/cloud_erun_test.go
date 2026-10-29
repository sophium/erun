package providerconfig

import (
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

func TestERunProviderSupportsOIDCIssuerAggregation(t *testing.T) {
	if !cloudProviderSupportsOIDC(configpkg.CloudProviderERun) {
		t.Fatal("cloudProviderSupportsOIDC(erun) = false, want true")
	}
}
