package eruncommon

import (
	"fmt"
	"io"
	"strings"
	"testing"
)

// TestCloudContextPowerErrorClassification pins the actionable-error contract
// for stop/start failures (issue #456: a failed Stop surfaced as a bare
// "exit status 1" while the instance kept running). These branches only fire
// on real AWS error responses, which the dry-run integration harness cannot
// produce — the same structural gap erun-integration/AGENTS.md records for
// the AWS error helpers — so a white-box test with an injected RunAWS owns
// the contract, mirroring deploy_persist_runtime_version_test.go.
func TestCloudContextPowerErrorClassification(t *testing.T) {
	ctx := Context{Logger: NewLoggerWithWriters(VerbosityInfo, io.Discard, io.Discard)}
	params := CloudContextParams{Name: "petios-ctx"}

	t.Run("stop blocked by stop protection names the lock and the unlock lever", func(t *testing.T) {
		raw := "aws ec2 stop-instances --instance-ids i-0abc123: An error occurred (OperationNotPermitted) when calling the StopInstances operation: The instance 'i-0abc123' may not be stopped. Modify its 'disableApiStop' instance attribute and try again."
		deps := CloudContextDependencies{RunAWS: failingRunAWS("stop-instances", raw)}
		_, err := StopCloudContext(ctx, powerErrorTestStore(), params, deps)
		assertErrorMentions(t, err, "expected the stop to fail", "stop protection", "enable-api-stop petios-ctx", "OperationNotPermitted")
	})

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

	t.Run("stop on an already-stopping instance is still absorbed into the wait", func(t *testing.T) {
		waited := false
		deps := CloudContextDependencies{RunAWS: alreadyStoppingRunAWS(&waited)}
		status, err := StopCloudContext(ctx, powerErrorTestStore(), params, deps)
		if err != nil {
			t.Fatalf("expected the already-stopping stop to succeed, got %v", err)
		}
		if !waited {
			t.Fatal("expected the stop to wait for instance-stopped")
		}
		if status.Status != CloudContextStatusStopped {
			t.Fatalf("status = %q, want %q", status.Status, CloudContextStatusStopped)
		}
	})

	t.Run("a failed stopped-wait reports the unobserved end state", func(t *testing.T) {
		deps := CloudContextDependencies{RunAWS: failingStopWaitRunAWS()}
		_, err := StopCloudContext(ctx, powerErrorTestStore(), params, deps)
		assertErrorMentions(t, err, "expected the wait failure to propagate", "not observed stopped", "Max attempts exceeded")
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

// assertErrorMentions fails when err is nil (reporting expectMsg) or when its
// message is missing any of the wanted substrings.
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

// alreadyStoppingRunAWS fails stop-instances with IncorrectInstanceState (the
// instance is already stopping) and succeeds the stopped-wait, flipping waited
// so the caller can assert the wait absorbed the race.
func alreadyStoppingRunAWS(waited *bool) func(Context, CloudProviderConfig, string, []string) (string, error) {
	return func(_ Context, _ CloudProviderConfig, _ string, args []string) (string, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "stop-instances") {
			return "", fmt.Errorf("aws ec2 stop-instances: An error occurred (IncorrectInstanceState) when calling the StopInstances operation")
		}
		if strings.Contains(joined, "wait instance-stopped") {
			*waited = true
			return "", nil
		}
		return "", nil
	}
}

// failingStopWaitRunAWS fails the stopped-wait waiter and lets every other AWS
// call succeed, modelling a stop that was issued but never observed stopped.
func failingStopWaitRunAWS() func(Context, CloudProviderConfig, string, []string) (string, error) {
	return func(_ Context, _ CloudProviderConfig, _ string, args []string) (string, error) {
		if strings.Contains(strings.Join(args, " "), "wait instance-stopped") {
			return "", fmt.Errorf("aws ec2 wait instance-stopped: Waiter InstanceStopped failed: Max attempts exceeded")
		}
		return "", nil
	}
}

// powerErrorTestStore returns the minimal store for the power-state paths:
// one provider alias and one managed context bound to it.
func powerErrorTestStore() CloudContextStore {
	return stubCloudContextStore{config: ERunConfig{
		CloudProviders: []CloudProviderConfig{{Alias: "dev-aws", Provider: CloudProviderAWS, Profile: "dev-profile"}},
		CloudContexts: []CloudContextConfig{{
			Name:               "petios-ctx",
			Provider:           CloudProviderAWS,
			CloudProviderAlias: "dev-aws",
			Region:             "eu-west-1",
			InstanceID:         "i-0abc123",
		}},
	}}
}

type stubCloudContextStore struct{ config ERunConfig }

func (s stubCloudContextStore) LoadERunConfig() (ERunConfig, string, error) { return s.config, "", nil }
func (s stubCloudContextStore) SaveERunConfig(ERunConfig) error             { return nil }

// failingRunAWS fails the named ec2 action with the given message and lets
// every other AWS call (waiters, association lookups) succeed.
func failingRunAWS(action, message string) func(Context, CloudProviderConfig, string, []string) (string, error) {
	return func(_ Context, _ CloudProviderConfig, _ string, args []string) (string, error) {
		if strings.Contains(strings.Join(args, " "), action) {
			return "", fmt.Errorf("%s", message)
		}
		return "", nil
	}
}
