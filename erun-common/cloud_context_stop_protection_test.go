package eruncommon

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestSetCloudContextStopProtectionEnablesDisableApiStop pins the
// recovery lever's "lock" path: calling SetCloudContextStopProtection
// with Enabled=true must invoke `aws ec2 modify-instance-attribute
// --disable-api-stop`. While the attribute is set every subsequent
// stop-instances call (the in-pod idle monitor, the user's Stop
// button, an external script) returns OperationNotPermitted, which is
// what keeps an unhealthy env up long enough to repair.
func TestSetCloudContextStopProtectionEnablesDisableApiStop(t *testing.T) {
	store := newCloudStoreForStateWait(t)
	var awsCalls []string
	status, err := SetCloudContextStopProtection(Context{}, store, CloudContextStopProtectionParams{
		Name:    "team-cloud",
		Enabled: true,
	}, CloudContextDependencies{
		Now: func() time.Time { return time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC) },
		RunAWS: func(_ Context, _ CloudProviderConfig, _ string, args []string) (string, error) {
			awsCalls = append(awsCalls, strings.Join(args, " "))
			return "", nil
		},
	})
	if err != nil {
		t.Fatalf("SetCloudContextStopProtection failed: %v", err)
	}
	if !joinedCallsContain(awsCalls, "ec2 modify-instance-attribute --instance-id i-test --disable-api-stop") {
		t.Fatalf("expected --disable-api-stop call, got %v", awsCalls)
	}
	if joinedCallsContain(awsCalls, "--no-disable-api-stop") {
		t.Fatalf("did not expect --no-disable-api-stop, got %v", awsCalls)
	}
	if !status.StopProtectionKnown || !status.StopProtection {
		t.Fatalf("expected StopProtectionKnown=true, StopProtection=true, got %+v", status)
	}
}

// TestSetCloudContextStopProtectionDisablesDisableApiStop pins the
// "unlock" path: Enabled=false must invoke --no-disable-api-stop so
// AWS clears the attribute and normal auto-stop / manual-stop
// behaviour resumes.
func TestSetCloudContextStopProtectionDisablesDisableApiStop(t *testing.T) {
	store := newCloudStoreForStateWait(t)
	var awsCalls []string
	status, err := SetCloudContextStopProtection(Context{}, store, CloudContextStopProtectionParams{
		Name:    "team-cloud",
		Enabled: false,
	}, CloudContextDependencies{
		Now: func() time.Time { return time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC) },
		RunAWS: func(_ Context, _ CloudProviderConfig, _ string, args []string) (string, error) {
			awsCalls = append(awsCalls, strings.Join(args, " "))
			return "", nil
		},
	})
	if err != nil {
		t.Fatalf("SetCloudContextStopProtection failed: %v", err)
	}
	if !joinedCallsContain(awsCalls, "ec2 modify-instance-attribute --instance-id i-test --no-disable-api-stop") {
		t.Fatalf("expected --no-disable-api-stop call, got %v", awsCalls)
	}
	for _, call := range awsCalls {
		// The disable flag substring also appears inside --no-disable-api-stop,
		// so explicitly look for the unprefixed form to confirm absence.
		if strings.Contains(call, " --disable-api-stop") {
			t.Fatalf("did not expect --disable-api-stop on unlock path, got %v", awsCalls)
		}
	}
	if !status.StopProtectionKnown || status.StopProtection {
		t.Fatalf("expected StopProtectionKnown=true, StopProtection=false, got %+v", status)
	}
}

// TestSetCloudContextStopProtectionPropagatesAWSErrors locks the
// failure path: an AWS rejection (auth, malformed instance ID, etc.)
// must surface so the user knows the lock did not take effect. Silent
// success here would leave the user thinking the env is locked when
// the next idle tick is still free to call StopInstances.
func TestSetCloudContextStopProtectionPropagatesAWSErrors(t *testing.T) {
	store := newCloudStoreForStateWait(t)
	_, err := SetCloudContextStopProtection(Context{}, store, CloudContextStopProtectionParams{
		Name:    "team-cloud",
		Enabled: true,
	}, CloudContextDependencies{
		Now: func() time.Time { return time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC) },
		RunAWS: func(_ Context, _ CloudProviderConfig, _ string, args []string) (string, error) {
			if strings.Contains(strings.Join(args, " "), "modify-instance-attribute") {
				return "", fmt.Errorf("aws ec2 modify-instance-attribute: An error occurred (UnauthorizedOperation)")
			}
			return "", nil
		},
	})
	if err == nil {
		t.Fatal("expected UnauthorizedOperation to propagate")
	}
	if !strings.Contains(err.Error(), "UnauthorizedOperation") {
		t.Fatalf("expected UnauthorizedOperation in error, got %v", err)
	}
}

// TestDescribeCloudContextStopProtectionReadsAttribute pins the lazy
// read path used by the desktop's titlebar lock toggle to mirror the
// live AWS state without paying for it on every bulk refresh.
func TestDescribeCloudContextStopProtectionReadsAttribute(t *testing.T) {
	tests := []struct {
		name     string
		awsOut   string
		expected bool
	}{
		{name: "locked", awsOut: "True", expected: true},
		{name: "unlocked", awsOut: "False", expected: false},
		{name: "lowercase locked", awsOut: "true\n", expected: true},
		{name: "empty falls back to unlocked", awsOut: "", expected: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := newCloudStoreForStateWait(t)
			var awsCalls []string
			status, err := DescribeCloudContextStopProtection(Context{}, store, "team-cloud", CloudContextDependencies{
				Now: func() time.Time { return time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC) },
				RunAWS: func(_ Context, _ CloudProviderConfig, _ string, args []string) (string, error) {
					awsCalls = append(awsCalls, strings.Join(args, " "))
					return tc.awsOut, nil
				},
			})
			if err != nil {
				t.Fatalf("DescribeCloudContextStopProtection failed: %v", err)
			}
			if !joinedCallsContain(awsCalls, "ec2 describe-instance-attribute --instance-id i-test --attribute disableApiStop") {
				t.Fatalf("expected describe-instance-attribute call, got %v", awsCalls)
			}
			if !status.StopProtectionKnown {
				t.Fatalf("expected StopProtectionKnown=true, got %+v", status)
			}
			if status.StopProtection != tc.expected {
				t.Fatalf("expected StopProtection=%v, got %v", tc.expected, status.StopProtection)
			}
		})
	}
}

// TestSetCloudContextStopProtectionRejectsUnknownContext keeps the
// validation behaviour identical to Stop/Start: an unknown context
// name returns an actionable error rather than silently calling AWS
// with an empty instance ID.
func TestSetCloudContextStopProtectionRejectsUnknownContext(t *testing.T) {
	store := newCloudStoreForStateWait(t)
	_, err := SetCloudContextStopProtection(Context{}, store, CloudContextStopProtectionParams{
		Name:    "does-not-exist",
		Enabled: true,
	}, CloudContextDependencies{
		Now:    func() time.Time { return time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC) },
		RunAWS: func(Context, CloudProviderConfig, string, []string) (string, error) { return "", nil },
	})
	if err == nil {
		t.Fatal("expected unknown-context error")
	}
	if !strings.Contains(err.Error(), "is not configured") {
		t.Fatalf("expected 'is not configured' in error, got %v", err)
	}
}
