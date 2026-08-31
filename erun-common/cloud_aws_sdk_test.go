package eruncommon

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stsCallerIdentityFixture starts a fake STS endpoint that answers
// GetCallerIdentity with a fixed identity, honored via the SDK's own
// AWS_ENDPOINT_URL env var (no daemon, no real AWS account needed).
func stsCallerIdentityFixture(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, `<?xml version="1.0"?>
<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <GetCallerIdentityResult>
    <Arn>arn:aws:iam::123456789012:user/test-user</Arn>
    <UserId>AIDAEXAMPLE</UserId>
    <Account>123456789012</Account>
  </GetCallerIdentityResult>
  <ResponseMetadata><RequestId>test-request-id</RequestId></ResponseMetadata>
</GetCallerIdentityResponse>`)
	}))
	t.Cleanup(server.Close)
	t.Setenv("AWS_ENDPOINT_URL", server.URL)
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAFAKE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "fakesecret")
	t.Setenv("AWS_REGION", "us-east-1")
}

func traceCapturingContext() (Context, *bytes.Buffer) {
	var trace bytes.Buffer
	logger := NewLogger(VerbosityInfo).WithTraceSink(&trace)
	return Context{Logger: logger}, &trace
}

func TestLibraryResolveAWSIdentityMatchesSubprocessObservableResult(t *testing.T) {
	stsCallerIdentityFixture(t)
	ctx, trace := traceCapturingContext()

	identity, err := libraryResolveAWSIdentity(ctx, "")
	if err != nil {
		t.Fatalf("libraryResolveAWSIdentity: %v", err)
	}

	// This is exactly the shape defaultResolveAWSIdentity parses out of `aws
	// sts get-caller-identity --output json` stdout — the two paths must
	// agree on the observable result for the same underlying identity.
	want := AWSIdentity{Account: "123456789012", Arn: "arn:aws:iam::123456789012:user/test-user", UserID: "AIDAEXAMPLE"}
	if identity != want {
		t.Fatalf("identity = %+v, want %+v", identity, want)
	}

	// The trace line must be byte-identical to what defaultResolveAWSIdentity
	// renders for the subprocess path (root AGENTS.md's dry-run/audit
	// contract: the equivalent CLI invocation, regardless of execution mode).
	wantTrace := formatShellCommand("", "aws", awsGetCallerIdentityArgs("")...)
	if got := strings.TrimSpace(trace.String()); got != wantTrace {
		t.Fatalf("trace = %q, want %q", got, wantTrace)
	}
}

// TestAWSGetCallerIdentityArgsSharedByBothPaths locks the one argv builder
// defaultResolveAWSIdentity (subprocess) and traceAWSGetCallerIdentity
// (library) both call, so a profile can never render differently between
// execution modes.
func TestAWSGetCallerIdentityArgsSharedByBothPaths(t *testing.T) {
	got := awsGetCallerIdentityArgs("my-profile")
	want := []string{"sts", "get-caller-identity", "--output", "json", "--profile", "my-profile"}
	if len(got) != len(want) {
		t.Fatalf("args = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("args = %v, want %v", got, want)
		}
	}
}

func TestLibraryCheckAWSStatusActive(t *testing.T) {
	stsCallerIdentityFixture(t)
	provider := CloudProviderConfig{Provider: CloudProviderAWS, Alias: "test@aws"}

	status := libraryCheckAWSStatus(Context{}, provider)

	if status.Status != CloudTokenStatusActive {
		t.Fatalf("status = %+v, want Active", status)
	}
}

func TestLibraryCheckAWSStatusNotConfiguredForMissingProfile(t *testing.T) {
	provider := CloudProviderConfig{Provider: CloudProviderAWS, Alias: "test@aws", Profile: "erun-cloud-aws-sdk-test-missing-profile"}

	status := libraryCheckAWSStatus(Context{}, provider)

	if status.Status != CloudTokenStatusNotConfigured {
		t.Fatalf("status = %+v, want NotConfigured", status)
	}
	if status.Message == "" {
		t.Fatal("expected a non-empty status message")
	}
}

func TestIsAWSProfileNotConfiguredError(t *testing.T) {
	_, err := awsSDKCallerIdentity(context.Background(), "erun-cloud-aws-sdk-test-missing-profile")
	if err == nil {
		t.Fatal("expected an error for a profile absent from any shared config file")
	}
	if !isAWSProfileNotConfiguredError(err) {
		t.Fatalf("isAWSProfileNotConfiguredError(%v) = false, want true", err)
	}
}
