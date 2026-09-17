package cmd

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common"
)

const contextListSSOError = "aws: [ERROR]: The SSO session associated with this profile has expired or is otherwise invalid. To refresh this SSO session run aws sso login with the corresponding profile."

type contextListTestStore struct{ config eruncommon.ERunConfig }

func (s contextListTestStore) LoadERunConfig() (eruncommon.ERunConfig, string, error) {
	return s.config, "", nil
}

func (s contextListTestStore) SaveERunConfig(eruncommon.ERunConfig) error { return nil }

func contextListTestContexts(regions ...string) eruncommon.ERunConfig {
	contexts := make([]eruncommon.CloudContextConfig, 0, len(regions))
	for i, region := range regions {
		contexts = append(contexts, eruncommon.CloudContextConfig{
			Name:               fmt.Sprintf("erun-%03d", i+1),
			Provider:           eruncommon.CloudProviderAWS,
			CloudProviderAlias: "dev-aws",
			Region:             region,
			InstanceID:         fmt.Sprintf("i-%03d", i+1),
			InstanceType:       "c8gd.2xlarge",
			DiskSizeGB:         100,
			DiskType:           "gp3",
		})
	}
	return eruncommon.ERunConfig{
		CloudProviders: []eruncommon.CloudProviderConfig{{Alias: "dev-aws", Provider: eruncommon.CloudProviderAWS, Profile: "dev-profile"}},
		CloudContexts:  contexts,
	}
}

func runContextListForTest(t *testing.T, regions []string, runAWS func(eruncommon.Context, eruncommon.CloudProviderConfig, string, []string) (string, error)) string {
	t.Helper()
	var out bytes.Buffer
	ctx := eruncommon.Context{Stdout: &out}
	store := contextListTestStore{config: contextListTestContexts(regions...)}
	deps := eruncommon.CloudContextDependencies{RunAWS: runAWS}
	if err := runContextListCommand(ctx, store, deps); err != nil {
		t.Fatalf("context list failed: %v", err)
	}
	return out.String()
}

// TestContextListReportsOneBatchedRefreshFailure pins what the operator reads.
// The status refresh is one provider call per (alias, region); when it fails,
// every context in that batch is Unknown for the same reason. Reporting that
// reason per row repeated the full command and error once per context, burying
// the per-context detail the operator actually came for.
func TestContextListReportsOneBatchedRefreshFailure(t *testing.T) {
	out := runContextListForTest(t, []string{"eu-west-2", "eu-west-2", "eu-west-2", "eu-west-2"},
		func(_ eruncommon.Context, _ eruncommon.CloudProviderConfig, _ string, _ []string) (string, error) {
			return "", fmt.Errorf("%s", contextListSSOError)
		},
	)

	if got := strings.Count(out, contextListSSOError); got != 1 {
		t.Fatalf("the failure appears %d times, want 1:\n%s", got, out)
	}
	if got := strings.Count(out, "status=unknown"); got != 4 {
		t.Fatalf("got %d unknown rows, want 4:\n%s", got, out)
	}
	// The failure is still reported -- one message, above the rows -- and it
	// keeps the upstream remedy the operator needs.
	if !strings.Contains(out, "status refresh failed") {
		t.Fatalf("the refresh failure was swallowed:\n%s", out)
	}
	if !strings.Contains(out, "run aws sso login") {
		t.Fatalf("the reported failure lost the upstream remedy:\n%s", out)
	}
	// Per-row detail the operator came for survives.
	for _, want := range []string{"erun-001", "erun-004", "i-004", "type=c8gd.2xlarge"} {
		if !strings.Contains(out, want) {
			t.Errorf("row detail %q missing:\n%s", want, out)
		}
	}
}

// TestContextListReportsEachDistinctRefreshFailure is the other half of the
// property: collapsing a shared cause must not collapse separate ones.
func TestContextListReportsEachDistinctRefreshFailure(t *testing.T) {
	regions := []string{"eu-west-1", "eu-west-2", "us-east-1"}
	out := runContextListForTest(t, regions,
		func(_ eruncommon.Context, _ eruncommon.CloudProviderConfig, region string, _ []string) (string, error) {
			return "", fmt.Errorf("%s (region %s)", contextListSSOError, region)
		},
	)

	if got := strings.Count(out, "status refresh failed"); got != len(regions) {
		t.Fatalf("got %d reported failures, want %d:\n%s", got, len(regions), out)
	}
	for _, region := range regions {
		if !strings.Contains(out, "region="+region+")") {
			t.Errorf("failure for region %s was swallowed:\n%s", region, out)
		}
	}
}

// TestContextListKeepsPerContextDetailWhenTheBatchSucceeds pins that the
// batched lines are additive: a healthy refresh still reports live state, and a
// per-context fact still keeps its own message.
func TestContextListKeepsPerContextDetailWhenTheBatchSucceeds(t *testing.T) {
	out := runContextListForTest(t, []string{"eu-west-2", "eu-west-2"},
		func(_ eruncommon.Context, _ eruncommon.CloudProviderConfig, _ string, _ []string) (string, error) {
			return "i-001 running\n", nil
		},
	)

	if strings.Contains(out, "status refresh failed") {
		t.Fatalf("a healthy batch reported a failure:\n%s", out)
	}
	if !strings.Contains(out, "status=running") {
		t.Fatalf("live state missing:\n%s", out)
	}
	if !strings.Contains(out, "message=instance not found in AWS") {
		t.Fatalf("the absent instance lost its per-context message:\n%s", out)
	}
}
