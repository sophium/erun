package eruncommon

import (
	"encoding/json"
	"testing"
)

// unconfiguredStore is the state the reported defect is about: a store that
// resolves fine and has no cloud providers and no cloud contexts in it. The
// listing still succeeds, and the question is only what an empty listing
// serialises as.
type unconfiguredStore struct{}

func (unconfiguredStore) LoadERunConfig() (ERunConfig, string, error) {
	return ERunConfig{}, "", nil
}

// TestCloudProviderListStatusesIsNeverNil pins the producers' half of the
// collection shape, so the field's own contract can be asserted against the
// real type at the transport that owns it instead of against a copy.
func TestCloudProviderListStatusesIsNeverNil(t *testing.T) {
	statuses, err := ListCloudProviderStatuses(unconfiguredStore{}, CloudDependencies{})
	if err != nil {
		t.Fatalf("list cloud provider statuses: %v", err)
	}
	if statuses == nil {
		t.Fatal("an environment with no aliases produced a nil slice; the field then " +
			"serialises as null, which a caller cannot tell from a listing that was never taken")
	}
}

// TestCloudContextListResultCarriesAnEmptyArray pins the shape of the type the
// CLI's `context list` and the context_list MCP tool share, including the
// contrast that justifies leaving the sibling field alone: RefreshFailures is
// an annotation on the rows, and it stays omitted when the refresh was never
// attempted, because an empty array there would assert that it ran and found
// nothing to report -- a different fact from never having looked.
func TestCloudContextListResultCarriesAnEmptyArray(t *testing.T) {
	result, err := RefreshCloudContextList(Context{}, unconfiguredStore{}, CloudContextDependencies{})
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
