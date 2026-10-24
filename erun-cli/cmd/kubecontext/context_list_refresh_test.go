package kubecontext

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// listRefreshTestStore is the smallest CloudContextStore the list path needs:
// four contexts that all share one (alias, region) group, so a single
// describe-instances call covers every one of them.
type listRefreshTestStore struct {
	config configpkg.ERunConfig
}

func (s listRefreshTestStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return s.config, "", nil
}

func (s listRefreshTestStore) SaveERunConfig(configpkg.ERunConfig) error { return nil }

const (
	listRefreshTestAlias  = "dev"
	listRefreshTestRegion = "eu-west-2"
)

func listRefreshTestStoreWithIDs(instanceIDs ...string) listRefreshTestStore {
	contexts := make([]configpkg.CloudContextConfig, 0, len(instanceIDs))
	for i, instanceID := range instanceIDs {
		contexts = append(contexts, configpkg.CloudContextConfig{
			Name:               fmt.Sprintf("erun-%03d", i+1),
			Provider:           configpkg.CloudProviderAWS,
			CloudProviderAlias: listRefreshTestAlias,
			Region:             listRefreshTestRegion,
			InstanceID:         instanceID,
			InstanceType:       configpkg.DefaultCloudContextInstanceType,
			DiskType:           configpkg.DefaultCloudContextDiskType,
			DiskSizeGB:         configpkg.DefaultCloudContextDiskSizeGB,
			KubernetesContext:  fmt.Sprintf("erun-%03d", i+1),
		})
	}
	return listRefreshTestStore{config: configpkg.ERunConfig{
		CloudProviders: []configpkg.CloudProviderConfig{{
			Alias:    listRefreshTestAlias,
			Provider: configpkg.CloudProviderAWS,
		}},
		CloudContexts: contexts,
	}}
}

// listRefreshTestFailingRunAWS reproduces the upstream shape an expired SSO
// session produces: the aws CLI echoes the whole command it was given --
// including the comma-joined instance IDs of every context in the batch --
// before the error text.
func listRefreshTestFailingRunAWS() func(core.Context, configpkg.CloudProviderConfig, string, []string) (string, error) {
	return func(_ core.Context, _ configpkg.CloudProviderConfig, _ string, args []string) (string, error) {
		command := "aws " + strings.Join(args, " ")
		return "", fmt.Errorf("%s: aws: [ERROR]: The SSO session associated with this profile has expired or is otherwise invalid. To refresh this SSO session run aws sso login with the corresponding profile.", command)
	}
}

// The structured form owes the same fact once. A JSON consumer that only sees
// per-context messages would have to re-derive the batch from N identical
// copies -- and would read the batch's whole command, instance IDs included,
// in every row.
func TestContextListJSONCarriesTheBatchFailureOnce(t *testing.T) {
	instanceIDs := []string{"i-0aaaa11111aaaa1111", "i-0bbbb22222bbbb2222"}
	store := listRefreshTestStoreWithIDs(instanceIDs...)
	deps := cloud.CloudContextDependencies{RunAWS: listRefreshTestFailingRunAWS()}

	var buf bytes.Buffer
	ctx := core.Context{
		Output: core.OutputJSON,
		Stdout: &buf,
		Stderr: &buf,
		Logger: core.NewLoggerWithWriters(core.VerbosityInfo, io.Discard, io.Discard),
	}
	if err := runContextListCommand(ctx, store, deps); err != nil {
		t.Fatalf("context list: %v", err)
	}

	var result cloud.CloudContextListResult
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("decode %q: %v", buf.String(), err)
	}
	if len(result.RefreshFailures) != 1 {
		t.Fatalf("got %d refresh failures, want exactly 1: %s", len(result.RefreshFailures), buf.String())
	}
	failure := result.RefreshFailures[0]
	if failure.Alias != listRefreshTestAlias || failure.Region != listRefreshTestRegion {
		t.Fatalf("failure not attributed to its batch: %+v", failure)
	}
	if !strings.Contains(failure.Message, "SSO session") {
		t.Fatalf("failure does not carry the cause: %+v", failure)
	}
	if len(result.CloudContexts) != len(instanceIDs) {
		t.Fatalf("got %d contexts, want %d", len(result.CloudContexts), len(instanceIDs))
	}
	for _, context := range result.CloudContexts {
		requireUnknownWithoutSharedCause(t, context)
	}
}

// requireUnknownWithoutSharedCause pins what a row owes once its batch failed:
// the refuted state, and no copy of a cause that belongs to the batch.
func requireUnknownWithoutSharedCause(t *testing.T, context configpkg.CloudContextStatus) {
	t.Helper()
	if context.Message != "" {
		t.Fatalf("context %s repeats the shared cause: %q", context.Name, context.Message)
	}
	if context.Status != cloud.CloudContextStatusUnknown {
		t.Fatalf("context %s status = %q, want %q", context.Name, context.Status, cloud.CloudContextStatusUnknown)
	}
}
