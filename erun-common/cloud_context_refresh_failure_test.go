package eruncommon

import (
	"fmt"
	"io"
	"strings"
	"testing"
)

// The status refresh is one provider call per (alias, region), so its failures
// belong to the batch rather than to any one context. These tests pin the
// contract that keeps that shape honest in both directions: one failed batch is
// reported once however many contexts it covered, and distinct failed batches
// are all still reported.
const refreshFailureSSOError = "aws: [ERROR]: The SSO session associated with this profile has expired or is otherwise invalid. To refresh this SSO session run aws sso login with the corresponding profile."

func refreshFailureTestContexts(regions ...string) []CloudContextConfig {
	contexts := make([]CloudContextConfig, 0, len(regions))
	for i, region := range regions {
		contexts = append(contexts, CloudContextConfig{
			Name:               fmt.Sprintf("erun-%03d", i+1),
			Provider:           CloudProviderAWS,
			CloudProviderAlias: "dev-aws",
			Region:             region,
			InstanceID:         fmt.Sprintf("i-%03d", i+1),
		})
	}
	return contexts
}

func refreshFailureTestStore(contexts ...CloudContextConfig) CloudContextStore {
	return stubCloudContextStore{config: ERunConfig{
		CloudProviders: []CloudProviderConfig{{Alias: "dev-aws", Provider: CloudProviderAWS, Profile: "dev-profile"}},
		CloudContexts:  contexts,
	}}
}

func refreshFailureTestContext() Context {
	return Context{Logger: NewLoggerWithWriters(VerbosityInfo, io.Discard, io.Discard)}
}

// TestCloudContextRefreshFailureReportedOncePerBatch pins the shape the
// operator reads: four contexts sharing one unreachable batch are Unknown for
// one reason, and that reason is stated once.
func TestCloudContextRefreshFailureReportedOncePerBatch(t *testing.T) {
	store := refreshFailureTestStore(refreshFailureTestContexts("eu-west-2", "eu-west-2", "eu-west-2", "eu-west-2")...)
	deps := CloudContextDependencies{RunAWS: alwaysFailingRunAWS(refreshFailureSSOError)}
	statuses, err := RefreshCloudContextStatuses(refreshFailureTestContext(), store, deps)
	if err != nil {
		t.Fatalf("refresh returned an error: %v", err)
	}
	if len(statuses) != 4 {
		t.Fatalf("got %d statuses, want 4", len(statuses))
	}

	failures := CloudContextRefreshFailures(statuses)
	if len(failures) != 1 {
		t.Fatalf("got %d reported failures, want 1: %+v", len(failures), failures)
	}
	if got := strings.Count(failures[0].Summary(), refreshFailureSSOError); got != 1 {
		t.Fatalf("the failure summary repeats the cause %d times: %q", got, failures[0].Summary())
	}
	if len(failures[0].ContextNames) != 4 {
		t.Fatalf("the failure covers %d contexts, want 4: %v", len(failures[0].ContextNames), failures[0].ContextNames)
	}
}

// TestCloudContextRefreshFailureKeepsRowsUnknownAndHonest pins what each row
// still says on its own: the state is unknown rather than guessed, the row does
// not restate the shared cause, and the cause stays reachable from the row so
// no surface loses it.
func TestCloudContextRefreshFailureKeepsRowsUnknownAndHonest(t *testing.T) {
	store := refreshFailureTestStore(refreshFailureTestContexts("eu-west-2", "eu-west-2")...)
	deps := CloudContextDependencies{RunAWS: alwaysFailingRunAWS(refreshFailureSSOError)}
	statuses, err := RefreshCloudContextStatuses(refreshFailureTestContext(), store, deps)
	if err != nil {
		t.Fatalf("refresh returned an error: %v", err)
	}

	for _, status := range statuses {
		if status.Status != CloudContextStatusUnknown {
			t.Errorf("%s status = %q, want %q", status.Name, status.Status, CloudContextStatusUnknown)
		}
		if status.Message != "" {
			t.Errorf("%s repeats the shared cause in its own message: %q", status.Name, status.Message)
		}
		if !strings.Contains(CloudContextStatusMessage(status), refreshFailureSSOError) {
			t.Errorf("%s no longer surfaces the failure at all: %q", status.Name, CloudContextStatusMessage(status))
		}
	}
}

// TestCloudContextDistinctRefreshFailuresStayDistinct is the other half of the
// property: collapsing a shared cause must not collapse separate ones.
func TestCloudContextDistinctRefreshFailuresStayDistinct(t *testing.T) {
	regions := []string{"eu-west-1", "eu-west-2", "us-east-1", "ap-south-1"}
	store := refreshFailureTestStore(refreshFailureTestContexts(regions...)...)
	deps := CloudContextDependencies{RunAWS: regionFailingRunAWS(refreshFailureSSOError)}
	statuses, err := RefreshCloudContextStatuses(refreshFailureTestContext(), store, deps)
	if err != nil {
		t.Fatalf("refresh returned an error: %v", err)
	}

	failures := CloudContextRefreshFailures(statuses)
	if len(failures) != len(regions) {
		t.Fatalf("got %d reported failures, want %d: %+v", len(failures), len(regions), failures)
	}
	seen := make(map[string]bool, len(failures))
	for _, failure := range failures {
		if seen[failure.Region] {
			t.Errorf("region %s reported more than once", failure.Region)
		}
		seen[failure.Region] = true
	}
	for _, region := range regions {
		if !seen[region] {
			t.Errorf("failure for region %s was swallowed", region)
		}
	}
}

// TestCloudContextRefreshFailureIsScopedToItsBatch pins that a failure is
// attributed only to the batch that failed: a healthy batch keeps its live
// states, and a per-context fact such as an absent instance keeps its own
// message instead of being replaced by the batch's.
func TestCloudContextRefreshFailureIsScopedToItsBatch(t *testing.T) {
	store := refreshFailureTestStore(refreshFailureTestContexts("eu-west-2", "eu-west-2", "us-east-1")...)
	deps := CloudContextDependencies{RunAWS: ssoFailingUsEastRunAWS()}
	statuses, err := RefreshCloudContextStatuses(refreshFailureTestContext(), store, deps)
	if err != nil {
		t.Fatalf("refresh returned an error: %v", err)
	}

	failures := CloudContextRefreshFailures(statuses)
	if len(failures) != 1 {
		t.Fatalf("got %d reported failures, want 1: %+v", len(failures), failures)
	}
	if failures[0].Region != "us-east-1" {
		t.Fatalf("failure attributed to region %q, want us-east-1", failures[0].Region)
	}
	if got := statuses[0].Status; got != CloudContextStatusRunning {
		t.Errorf("erun-001 status = %q, want %q", got, CloudContextStatusRunning)
	}
	if got := statuses[0].Message; got != "" {
		t.Errorf("erun-001 message = %q, want empty", got)
	}
	if got := statuses[1].Status; got != CloudContextStatusUnknown {
		t.Errorf("erun-002 status = %q, want %q", got, CloudContextStatusUnknown)
	}
	if got := statuses[1].Message; got != "instance not found in AWS" {
		t.Errorf("erun-002 lost its per-context detail: %q", got)
	}
	if statuses[1].RefreshFailure != nil {
		t.Error("erun-002 was attributed a batch failure that did not affect it")
	}
	if !strings.Contains(CloudContextStatusMessage(statuses[2]), refreshFailureSSOError) {
		t.Errorf("erun-003 no longer surfaces its batch failure: %q", CloudContextStatusMessage(statuses[2]))
	}
}

// TestCloudContextWithoutRefreshGetsNoFailure pins that a context with no
// instance to refresh is not handed a failure it was never part of.
func TestCloudContextWithoutRefreshGetsNoFailure(t *testing.T) {
	contexts := refreshFailureTestContexts("eu-west-2")
	contexts = append(contexts, CloudContextConfig{
		Name:               "erun-002",
		Provider:           CloudProviderAWS,
		CloudProviderAlias: "dev-aws",
		Region:             "eu-west-2",
	})
	store := refreshFailureTestStore(contexts...)
	deps := CloudContextDependencies{RunAWS: alwaysFailingRunAWS(refreshFailureSSOError)}
	statuses, err := RefreshCloudContextStatuses(refreshFailureTestContext(), store, deps)
	if err != nil {
		t.Fatalf("refresh returned an error: %v", err)
	}

	if got := CloudContextRefreshFailures(statuses); len(got) != 1 {
		t.Fatalf("got %d reported failures, want 1: %+v", len(got), got)
	}
	if statuses[1].RefreshFailure != nil || statuses[1].Message != "" {
		t.Errorf("unrefreshed context picked up a failure: %+v", statuses[1])
	}
}

func alwaysFailingRunAWS(message string) func(Context, CloudProviderConfig, string, []string) (string, error) {
	return func(_ Context, _ CloudProviderConfig, _ string, _ []string) (string, error) {
		return "", fmt.Errorf("%s", message)
	}
}

// regionFailingRunAWS fails each region's batch with that region's own error,
// so a test can tell one reported failure from another.
func regionFailingRunAWS(message string) func(Context, CloudProviderConfig, string, []string) (string, error) {
	return func(_ Context, _ CloudProviderConfig, region string, _ []string) (string, error) {
		return "", fmt.Errorf("%s (region %s)", message, region)
	}
}

// ssoFailingUsEastRunAWS fails only the us-east-1 batch, leaving the eu-west-2
// response to carry its own per-context detail.
func ssoFailingUsEastRunAWS() func(Context, CloudProviderConfig, string, []string) (string, error) {
	return func(_ Context, _ CloudProviderConfig, region string, _ []string) (string, error) {
		if region == "us-east-1" {
			return "", fmt.Errorf("%s", refreshFailureSSOError)
		}
		return "i-001 running\n", nil
	}
}
