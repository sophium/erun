package contextpower

import (
	"encoding/json"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud/cloudcontext"
)

// unconfiguredStore has no cloud providers and no cloud contexts.
type unconfiguredStore struct{}

func (unconfiguredStore) LoadERunConfig() (config.ERunConfig, string, error) {
	return config.ERunConfig{}, "", nil
}

// RefreshFailures stays omitted: the refresh was never attempted.
func TestCloudContextListResultCarriesAnEmptyArray(t *testing.T) {
	result, err := RefreshCloudContextList(core.Context{}, unconfiguredStore{}, cloudcontext.CloudContextDependencies{})
	if err != nil {
		t.Fatalf("refresh cloud context list: %v", err)
	}
	if result.CloudContexts == nil {
		t.Fatal("an environment with no cloud contexts produced a nil slice")
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(encoded) != `{"cloudContexts":[]}` {
		t.Fatalf("an environment with no cloud contexts serialised as %s", encoded)
	}

	var document map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("structured result is not a JSON object: %v", err)
	}
	if _, present := document["refreshFailures"]; present {
		t.Fatalf("the refresh annotation was filled in, asserting a refresh that never ran: %s", encoded)
	}
}
