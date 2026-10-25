package providertoken

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// unconfiguredStore has no cloud providers and no cloud contexts.
type unconfiguredStore struct{}

func (unconfiguredStore) LoadERunConfig() (config.ERunConfig, string, error) {
	return config.ERunConfig{}, "", nil
}

func TestCloudProviderListStatusesIsNeverNil(t *testing.T) {
	statuses, err := ListCloudProviderStatuses(unconfiguredStore{}, cloud.CloudDependencies{})
	if err != nil {
		t.Fatalf("list cloud provider statuses: %v", err)
	}
	if statuses == nil {
		t.Fatal("an environment with no aliases produced a nil slice; the field then " +
			"serialises as null, which a caller cannot tell from a listing that was never taken")
	}
}
