package cloud

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

func traceCapturingContext() (core.Context, *bytes.Buffer) {
	var trace bytes.Buffer
	logger := core.NewLogger(core.VerbosityInfo).WithTraceSink(&trace)
	return core.Context{Logger: logger}, &trace
}

func TestLibraryCheckAWSStatusNotConfiguredForMissingProfile(t *testing.T) {
	provider := model.CloudProviderConfig{Provider: config.CloudProviderAWS, Alias: "test@aws", Profile: "erun-cloud-aws-sdk-test-missing-profile"}

	status := libraryCheckAWSStatus(core.Context{}, provider)

	if status.Status != config.CloudTokenStatusNotConfigured {
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

func TestLibraryRunAWSExportCredentialsMissingProfile(t *testing.T) {
	ctx, _ := traceCapturingContext()

	_, err := libraryRunAWSExportCredentials(ctx, "erun-cloud-aws-sdk-test-missing-profile")
	if err == nil {
		t.Fatal("expected an error for a profile absent from any shared config file")
	}
	if !strings.Contains(err.Error(), "export AWS credentials:") {
		t.Fatalf("err = %q, want it wrapped as \"export AWS credentials: ...\"", err)
	}
}
