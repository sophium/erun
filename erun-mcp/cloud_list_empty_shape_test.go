package erunmcp

import (
	"encoding/json"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// unconfiguredStore is the state the reported defect is about: a store that
// resolves fine and has no cloud aliases and no cloud contexts in it.
type unconfiguredStore struct{}

func (unconfiguredStore) LoadERunConfig() (eruncommon.ERunConfig, string, error) {
	return eruncommon.ERunConfig{}, "", nil
}

// TestCollectionToolsReportAnEmptyCollectionAsAnArray pins both tools the
// report measured answering an empty environment with a bare `{}`. The key was
// absent, not null, so a caller reading result.contexts.length threw where the
// sibling idle_stop_history and activity_lease_list tools returned 0 -- and
// there was no way to tell "no aliases are configured", the diagnosis
// cloud_list exists to confirm, from "this tool does not report aliases".
//
// The two builders behind these fields already returned a non-nil slice, so
// this is the field's own contract and not the builder's: omitempty drops a
// slice of length zero exactly as it drops a nil one, and initialising the
// slice without dropping the tag would have changed nothing.
func TestCollectionToolsReportAnEmptyCollectionAsAnArray(t *testing.T) {
	providers, err := eruncommon.ListCloudProviderStatuses(unconfiguredStore{}, eruncommon.CloudDependencies{})
	if err != nil {
		t.Fatalf("list cloud provider statuses: %v", err)
	}
	contexts, err := eruncommon.RefreshCloudContextList(
		eruncommon.Context{}, unconfiguredStore{}, eruncommon.CloudContextDependencies{})
	if err != nil {
		t.Fatalf("refresh cloud context list: %v", err)
	}

	cases := []struct {
		tool  string
		value any
		field string
	}{
		{"cloud_list", CloudListResult{CloudProviders: providers}, "cloudProviders"},
		{"context_list", ContextListResult{CloudContexts: contexts.CloudContexts}, "cloudContexts"},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			encoded, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("marshal result: %v", err)
			}

			var document map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &document); err != nil {
				t.Fatalf("result is not a JSON object: %v (%s)", err, encoded)
			}
			raw, present := document[tc.field]
			if !present {
				t.Fatalf("%s answered %s: the collection field is absent, so a caller "+
					"cannot tell an empty collection from a field this tool does not report",
					tc.tool, encoded)
			}
			var rows []json.RawMessage
			if err := json.Unmarshal(raw, &rows); err != nil {
				t.Fatalf("%s reported %q for %s, which is not an array: %v", tc.tool, raw, tc.field, err)
			}
			if rows == nil {
				t.Fatalf("%s reported %q for %s, which is null rather than []", tc.tool, raw, tc.field)
			}
			if len(rows) != 0 {
				t.Fatalf("expected an empty collection, got %d rows", len(rows))
			}
		})
	}
}
