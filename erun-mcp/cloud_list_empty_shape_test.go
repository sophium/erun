package erunmcp

import (
	"encoding/json"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/cloudcontext"
	"github.com/sophium/erun/erun-common/infra/cloud/contextpower"
	"github.com/sophium/erun/erun-common/infra/cloud/providertoken"
	cloudpkg "github.com/sophium/erun/erun-mcp/tools/cloud"
	"github.com/sophium/erun/erun-mcp/tools/kubecontext"
)

type unconfiguredStore struct{}

func (unconfiguredStore) LoadERunConfig() (config.ERunConfig, string, error) {
	return config.ERunConfig{}, "", nil
}

// An empty collection must serialize as [] not be omitted; omitempty drops empty slices
// as well as nil ones.
func TestCollectionToolsReportAnEmptyCollectionAsAnArray(t *testing.T) {
	providers, err := providertoken.ListCloudProviderStatuses(unconfiguredStore{}, cloud.CloudDependencies{})
	if err != nil {
		t.Fatalf("list cloud provider statuses: %v", err)
	}
	contexts, err := contextpower.RefreshCloudContextList(
		core.Context{}, unconfiguredStore{}, cloudcontext.CloudContextDependencies{})
	if err != nil {
		t.Fatalf("refresh cloud context list: %v", err)
	}

	cases := []struct {
		tool  string
		value any
		field string
	}{
		{"cloud_list", cloudpkg.CloudListResult{CloudProviders: providers}, "cloudProviders"},
		{"context_list", kubecontext.ContextListResult{CloudContexts: contexts.CloudContexts}, "cloudContexts"},
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
