package eruncommon

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestStopCloudContextWaitsForInstanceStopped locks the AWS-state
// follow-through gap fixed in issue #361. Before the fix,
// StopCloudContext returned the moment AWS *accepted* the
// stop-instances call, even though the EC2 then needed another
// 30-60 s to actually reach the `stopped` state. Callers that ran
// a follow-up StartCloudContext inside that window hit AWS's
// IncorrectInstanceState rejection. The fix runs `aws ec2 wait
// instance-stopped` so the function only returns once the live
// state matches the promise.
func TestStopCloudContextWaitsForInstanceStopped(t *testing.T) {
	store := newCloudStoreForStateWait(t)
	var awsCalls []string
	_, err := StopCloudContext(Context{}, store, CloudContextParams{Name: "team-cloud"}, CloudContextDependencies{
		Now: func() time.Time { return time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC) },
		RunAWS: func(_ Context, _ CloudProviderConfig, _ string, args []string) (string, error) {
			awsCalls = append(awsCalls, strings.Join(args, " "))
			return "", nil
		},
		RunKubectl: func(Context, []string) error { return nil },
	})
	if err != nil {
		t.Fatalf("StopCloudContext failed: %v", err)
	}
	if !joinedCallsContain(awsCalls, "ec2 stop-instances") {
		t.Fatalf("expected stop-instances call in %v", awsCalls)
	}
	if !joinedCallsContain(awsCalls, "ec2 wait instance-stopped") {
		t.Fatalf("expected wait instance-stopped follow-up in %v", awsCalls)
	}
	// Order matters: the wait must come after the stop request so the
	// function does not block on a stop that AWS never received.
	stopIdx := indexOfCallContaining(awsCalls, "ec2 stop-instances")
	waitIdx := indexOfCallContaining(awsCalls, "ec2 wait instance-stopped")
	if !(stopIdx >= 0 && waitIdx > stopIdx) {
		t.Fatalf("expected wait to follow stop, got order %v (stopIdx=%d waitIdx=%d)", awsCalls, stopIdx, waitIdx)
	}
}

// TestStopCloudContextTreatsIncorrectInstanceStateAsAlreadyNotRunning
// pins the recovery branch that lets callers retry a stop after the
// instance is already shutting down. AWS rejects stop-instances on
// an already-stopping or terminating instance with
// IncorrectInstanceState; the helper treats that as "intended end
// state in progress" and falls through to the wait so a redundant
// stop click does not raise an error.
func TestStopCloudContextTreatsIncorrectInstanceStateAsAlreadyNotRunning(t *testing.T) {
	store := newCloudStoreForStateWait(t)
	var awsCalls []string
	_, err := StopCloudContext(Context{}, store, CloudContextParams{Name: "team-cloud"}, CloudContextDependencies{
		Now: func() time.Time { return time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC) },
		RunAWS: func(_ Context, _ CloudProviderConfig, _ string, args []string) (string, error) {
			joined := strings.Join(args, " ")
			awsCalls = append(awsCalls, joined)
			if strings.Contains(joined, "ec2 stop-instances") {
				return "", fmt.Errorf("aws ec2 stop-instances --instance-ids i-test: An error occurred (IncorrectInstanceState) when calling the StopInstances operation")
			}
			return "", nil
		},
		RunKubectl: func(Context, []string) error { return nil },
	})
	if err != nil {
		t.Fatalf("StopCloudContext should swallow IncorrectInstanceState, got %v", err)
	}
	if idx := indexOfCallContaining(awsCalls, "ec2 wait instance-stopped"); idx < 0 {
		t.Fatalf("expected wait instance-stopped after IncorrectInstanceState, got %v", awsCalls)
	}
}

// TestStartCloudContextRecoversFromIncorrectInstanceState locks the
// retry path: when start-instances returns IncorrectInstanceState
// (the instance is still in `stopping`, etc.), the helper must
// `aws ec2 wait instance-stopped` and retry start-instances once.
// Without this, a click on a just-auto-stopped env raises a confusing
// AWS error and the desktop's reconnect loop spins pointlessly.
func TestStartCloudContextRecoversFromIncorrectInstanceState(t *testing.T) {
	store := newCloudStoreForStateWait(t)
	var awsCalls []string
	startAttempts := 0
	_, err := StartCloudContext(Context{}, store, CloudContextParams{Name: "team-cloud", Force: true}, CloudContextDependencies{
		Now: func() time.Time { return time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC) },
		RunAWS: func(_ Context, _ CloudProviderConfig, _ string, args []string) (string, error) {
			joined := strings.Join(args, " ")
			awsCalls = append(awsCalls, joined)
			if strings.Contains(joined, "ec2 start-instances") {
				startAttempts++
				if startAttempts == 1 {
					return "", fmt.Errorf("aws ec2 start-instances --instance-ids i-test: An error occurred (IncorrectInstanceState) when calling the StartInstances operation")
				}
				return "", nil
			}
			if strings.Contains(joined, "ec2 describe-instances") {
				return "198.51.100.10\n", nil
			}
			return "", nil
		},
		RunKubectl: func(Context, []string) error { return nil },
	})
	if err != nil {
		t.Fatalf("StartCloudContext should recover from IncorrectInstanceState, got %v", err)
	}
	if startAttempts != 2 {
		t.Fatalf("expected exactly one retry (2 start attempts), got %d", startAttempts)
	}
	firstStart := indexOfCallContaining(awsCalls, "ec2 start-instances")
	stoppedWait := indexOfCallContaining(awsCalls, "ec2 wait instance-stopped")
	runningWait := indexOfCallContaining(awsCalls, "ec2 wait instance-running")
	if !(firstStart >= 0 && stoppedWait > firstStart && runningWait > stoppedWait) {
		t.Fatalf("expected order start → wait instance-stopped → wait instance-running, got %v", awsCalls)
	}
}

// TestStartCloudContextDoesNotRetryOnOtherAWSErrors keeps the
// recovery branch tight: only IncorrectInstanceState triggers the
// wait+retry. An unrelated failure (auth, throttling, missing
// instance) must surface to the caller so the user can act on it
// instead of being swallowed by a silent retry loop.
func TestStartCloudContextDoesNotRetryOnOtherAWSErrors(t *testing.T) {
	store := newCloudStoreForStateWait(t)
	startAttempts := 0
	_, err := StartCloudContext(Context{}, store, CloudContextParams{Name: "team-cloud", Force: true}, CloudContextDependencies{
		Now: func() time.Time { return time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC) },
		RunAWS: func(_ Context, _ CloudProviderConfig, _ string, args []string) (string, error) {
			joined := strings.Join(args, " ")
			if strings.Contains(joined, "ec2 start-instances") {
				startAttempts++
				return "", fmt.Errorf("aws ec2 start-instances --instance-ids i-test: An error occurred (UnauthorizedOperation)")
			}
			return "", nil
		},
		RunKubectl: func(Context, []string) error { return nil },
	})
	if err == nil {
		t.Fatal("expected UnauthorizedOperation to propagate")
	}
	if !strings.Contains(err.Error(), "UnauthorizedOperation") {
		t.Fatalf("expected UnauthorizedOperation in error, got %v", err)
	}
	if startAttempts != 1 {
		t.Fatalf("expected no retry on non-IncorrectInstanceState error, got %d start attempts", startAttempts)
	}
}

func newCloudStoreForStateWait(t *testing.T) *memoryCloudStore {
	t.Helper()
	return &memoryCloudStore{
		config: ERunConfig{
			CloudProviders: []CloudProviderConfig{{
				Alias:    "team@aws",
				Provider: CloudProviderAWS,
			}},
			CloudContexts: []CloudContextConfig{{
				Name:               "team-cloud",
				KubernetesContext:  "team-cloud-kube",
				CloudProviderAlias: "team@aws",
				Region:             "eu-west-2",
				InstanceID:         "i-test",
				AdminToken:         "test-token",
			}},
		},
	}
}

func joinedCallsContain(calls []string, needle string) bool {
	return indexOfCallContaining(calls, needle) >= 0
}

func indexOfCallContaining(calls []string, needle string) int {
	for i, call := range calls {
		if strings.Contains(call, needle) {
			return i
		}
	}
	return -1
}
