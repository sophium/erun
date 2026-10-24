package cloud

import (
	"fmt"
	"io"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// TestCloudContextPowerErrorClassification pins the actionable-error contract
// for stop/start failures — a failed Stop once surfaced as a bare "exit
// status 1" while the instance kept running. The dry-run integration harness
// cannot produce the real AWS error responses these branches key on, so the
// contract lives here as a white-box test.
func TestCloudContextPowerErrorClassification(t *testing.T) {
	ctx := core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, io.Discard, io.Discard)}
	params := CloudContextParams{Name: "petios-ctx"}

	t.Run("stop with an expired AWS session names the login lever", func(t *testing.T) {
		raw := "aws ec2 stop-instances --instance-ids i-0abc123: Error when retrieving token from sso: Token has expired and refresh failed"
		deps := CloudContextDependencies{RunAWS: failingRunAWS("stop-instances", raw)}
		_, err := StopCloudContext(ctx, powerErrorTestStore(), params, deps)
		assertErrorMentions(t, err, "expected the stop to fail", "expired or unavailable", "erun cloud login --alias dev-aws", "Token has expired")
	})

	t.Run("stop with an unrelated AWS error passes through unchanged", func(t *testing.T) {
		raw := "aws ec2 stop-instances --instance-ids i-0abc123: An error occurred (InternalError) when calling the StopInstances operation"
		deps := CloudContextDependencies{RunAWS: failingRunAWS("stop-instances", raw)}
		_, err := StopCloudContext(ctx, powerErrorTestStore(), params, deps)
		if err == nil {
			t.Fatal("expected the stop to fail")
		}
		if got := err.Error(); got != raw {
			t.Fatalf("unrelated error was rewritten: %q", got)
		}
	})

	t.Run("start with an expired AWS session names the login lever", func(t *testing.T) {
		raw := "aws ec2 start-instances --instance-ids i-0abc123: An error occurred (ExpiredToken) when calling the StartInstances operation: The security token included in the request is expired"
		deps := CloudContextDependencies{RunAWS: failingRunAWS("start-instances", raw)}
		_, err := StartCloudContext(ctx, powerErrorTestStore(), params, deps)
		assertErrorMentions(t, err, "expected the start to fail", "erun cloud login --alias dev-aws")
	})

	t.Run("start never claims stop protection", func(t *testing.T) {
		raw := "aws ec2 start-instances --instance-ids i-0abc123: An error occurred (OperationNotPermitted) when calling the StartInstances operation"
		deps := CloudContextDependencies{RunAWS: failingRunAWS("start-instances", raw)}
		_, err := StartCloudContext(ctx, powerErrorTestStore(), params, deps)
		if err == nil {
			t.Fatal("expected the start to fail")
		}
		if strings.Contains(err.Error(), "stop protection") {
			t.Fatalf("start failure misclassified as stop protection: %q", err)
		}
	})
}

func assertErrorMentions(t *testing.T, err error, expectMsg string, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatal(expectMsg)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
}

func powerErrorTestStore() CloudContextStore {
	return stubCloudContextStore{config: configpkg.ERunConfig{
		CloudProviders: []configpkg.CloudProviderConfig{{Alias: "dev-aws", Provider: configpkg.CloudProviderAWS, Profile: "dev-profile"}},
		CloudContexts: []configpkg.CloudContextConfig{{
			Name:               "petios-ctx",
			Provider:           configpkg.CloudProviderAWS,
			CloudProviderAlias: "dev-aws",
			Region:             "eu-west-1",
			InstanceID:         "i-0abc123",
		}},
	}}
}

type stubCloudContextStore struct{ config configpkg.ERunConfig }

func (s stubCloudContextStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return s.config, "", nil
}
func (s stubCloudContextStore) SaveERunConfig(configpkg.ERunConfig) error { return nil }

func failingRunAWS(action, message string) func(core.Context, configpkg.CloudProviderConfig, string, []string) (string, error) {
	return func(_ core.Context, _ configpkg.CloudProviderConfig, _ string, args []string) (string, error) {
		if strings.Contains(strings.Join(args, " "), action) {
			return "", fmt.Errorf("%s", message)
		}
		return "", nil
	}
}
