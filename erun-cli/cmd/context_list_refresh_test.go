package cmd

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	common "github.com/sophium/erun/erun-common"
)

// listRefreshTestStore is the smallest CloudContextStore the list path needs:
// four contexts that all share one (alias, region) group, so a single
// describe-instances call covers every one of them.
type listRefreshTestStore struct {
	config common.ERunConfig
}

func (s listRefreshTestStore) LoadERunConfig() (common.ERunConfig, string, error) {
	return s.config, "", nil
}

func (s listRefreshTestStore) SaveERunConfig(common.ERunConfig) error { return nil }

const (
	listRefreshTestAlias  = "dev"
	listRefreshTestRegion = "eu-west-2"
)

func listRefreshTestStoreWithIDs(instanceIDs ...string) listRefreshTestStore {
	contexts := make([]common.CloudContextConfig, 0, len(instanceIDs))
	for i, instanceID := range instanceIDs {
		contexts = append(contexts, common.CloudContextConfig{
			Name:               fmt.Sprintf("erun-%03d", i+1),
			Provider:           common.CloudProviderAWS,
			CloudProviderAlias: listRefreshTestAlias,
			Region:             listRefreshTestRegion,
			InstanceID:         instanceID,
			InstanceType:       common.DefaultCloudContextInstanceType,
			DiskType:           common.DefaultCloudContextDiskType,
			DiskSizeGB:         common.DefaultCloudContextDiskSizeGB,
			KubernetesContext:  fmt.Sprintf("erun-%03d", i+1),
		})
	}
	return listRefreshTestStore{config: common.ERunConfig{
		CloudProviders: []common.CloudProviderConfig{{
			Alias:    listRefreshTestAlias,
			Provider: common.CloudProviderAWS,
		}},
		CloudContexts: contexts,
	}}
}

// listRefreshTestFailingRunAWS reproduces the upstream shape an expired SSO
// session produces: the aws CLI echoes the whole command it was given --
// including the comma-joined instance IDs of every context in the batch --
// before the error text.
func listRefreshTestFailingRunAWS() func(common.Context, common.CloudProviderConfig, string, []string) (string, error) {
	return func(_ common.Context, _ common.CloudProviderConfig, _ string, args []string) (string, error) {
		command := "aws " + strings.Join(args, " ")
		return "", fmt.Errorf("%s: aws: [ERROR]: The SSO session associated with this profile has expired or is otherwise invalid. To refresh this SSO session run aws sso login with the corresponding profile.", command)
	}
}

func runListRefreshTest(t *testing.T, store common.CloudContextStore, deps common.CloudContextDependencies) string {
	t.Helper()
	var buf bytes.Buffer
	ctx := common.Context{
		Stdout: &buf,
		Stderr: &buf,
		Logger: common.NewLoggerWithWriters(common.VerbosityInfo, io.Discard, io.Discard),
	}
	if err := runContextListCommand(ctx, store, deps); err != nil {
		t.Fatalf("context list: %v", err)
	}
	return buf.String()
}

// contextListRowLines returns only the per-context rows, so a batch-level line
// that legitimately names the whole batch's instance IDs is not mistaken for a
// row disclosing its siblings.
func contextListRowLines(out string) []string {
	var rows []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  - ") {
			rows = append(rows, line)
		}
	}
	return rows
}

// One failing describe-instances call covers every context in the group, so the
// operator has one problem to fix. Reporting it once per context repeats a
// several-hundred-character command N times and buries the per-context detail
// that actually differs; it also makes each row quote the instance IDs of every
// sibling context that shared the batch.
func TestContextListReportsBatchedRefreshFailureOnce(t *testing.T) {
	instanceIDs := []string{"i-0aaaa11111aaaa1111", "i-0bbbb22222bbbb2222", "i-0cccc33333cccc3333", "i-0dddd44444dddd4444"}
	store := listRefreshTestStoreWithIDs(instanceIDs...)
	deps := common.CloudContextDependencies{RunAWS: listRefreshTestFailingRunAWS()}

	out := runListRefreshTest(t, store, deps)

	if got := strings.Count(out, "status refresh failed"); got != 1 {
		t.Fatalf("status refresh failure appears %d times, want exactly once:\n%s", got, out)
	}

	rows := contextListRowLines(out)
	if len(rows) != len(instanceIDs) {
		t.Fatalf("got %d context rows, want %d:\n%s", len(rows), len(instanceIDs), out)
	}
	for _, row := range rows {
		own := ""
		for _, instanceID := range instanceIDs {
			if strings.Contains(row, instanceID) {
				own = instanceID
				break
			}
		}
		if own == "" {
			t.Fatalf("row does not name its own instance:\n%s", row)
		}
		for _, sibling := range instanceIDs {
			if sibling == own {
				continue
			}
			if strings.Contains(row, sibling) {
				t.Fatalf("row for %s discloses sibling instance %s:\n%s", own, sibling, row)
			}
		}
	}
}

// The failure must still be visible, once, at the level it occurred: a report
// that drops the cause entirely would trade repetition for silence, and silence
// reads as a successful refresh.
func TestContextListNamesTheBatchedRefreshFailureAtBatchLevel(t *testing.T) {
	store := listRefreshTestStoreWithIDs("i-0aaaa11111aaaa1111", "i-0bbbb22222bbbb2222")
	deps := common.CloudContextDependencies{RunAWS: listRefreshTestFailingRunAWS()}

	out := runListRefreshTest(t, store, deps)

	if !strings.Contains(out, "Cloud Contexts") {
		t.Fatalf("header missing:\n%s", out)
	}
	failureLine := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "status refresh failed") {
			failureLine = line
			break
		}
	}
	if failureLine == "" {
		t.Fatalf("no batch-level failure line:\n%s", out)
	}
	for _, want := range []string{"The SSO session associated with this profile has expired", listRefreshTestAlias, listRefreshTestRegion} {
		if !strings.Contains(failureLine, want) {
			t.Fatalf("batch-level failure line does not mention %q: %q", want, failureLine)
		}
	}
	for _, row := range contextListRowLines(out) {
		if strings.Contains(row, "message=") {
			t.Fatalf("row repeats the shared cause instead of leaving it to the batch line:\n%s", row)
		}
		if !strings.Contains(row, "status=unknown") {
			t.Fatalf("row does not report the refuted state as unknown:\n%s", row)
		}
	}
}

// A context-specific cause is not the shared one and must survive: dropping
// every message would lose the detail that actually differs per row.
func TestContextListKeepsPerContextMessages(t *testing.T) {
	store := listRefreshTestStoreWithIDs("i-0aaaa11111aaaa1111")
	deps := common.CloudContextDependencies{RunAWS: func(common.Context, common.CloudProviderConfig, string, []string) (string, error) {
		return "", nil
	}}

	out := runListRefreshTest(t, store, deps)

	rows := contextListRowLines(out)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1:\n%s", len(rows), out)
	}
	if !strings.Contains(rows[0], "message=") || !strings.Contains(rows[0], "not found in AWS") {
		t.Fatalf("per-context message was lost:\n%s", rows[0])
	}
}
