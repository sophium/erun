package eruncommon

import "testing"

func TestExecutionModeForDefaultsToSubprocess(t *testing.T) {
	config := ERunConfig{}
	if mode := ExecutionModeFor(config, "aws-sts"); mode != ExecutionModeSubprocess {
		t.Fatalf("mode = %q, want %q", mode, ExecutionModeSubprocess)
	}
}

func TestExecutionModeForLibraryOptIn(t *testing.T) {
	config := ERunConfig{Execution: ExecutionConfig{Modes: map[string]string{"aws-sts": "library"}}}
	if mode := ExecutionModeFor(config, "aws-sts"); mode != ExecutionModeLibrary {
		t.Fatalf("mode = %q, want %q", mode, ExecutionModeLibrary)
	}
}

func TestExecutionModeForUnrecognizedValueFallsBackToSubprocess(t *testing.T) {
	config := ERunConfig{Execution: ExecutionConfig{Modes: map[string]string{"aws-sts": "banana"}}}
	if mode := ExecutionModeFor(config, "aws-sts"); mode != ExecutionModeSubprocess {
		t.Fatalf("mode = %q, want %q", mode, ExecutionModeSubprocess)
	}
}

func TestExecutionModeForIsCaseInsensitive(t *testing.T) {
	config := ERunConfig{Execution: ExecutionConfig{Modes: map[string]string{"aws-sts": "LIBRARY"}}}
	if mode := ExecutionModeFor(config, "aws-sts"); mode != ExecutionModeLibrary {
		t.Fatalf("mode = %q, want %q", mode, ExecutionModeLibrary)
	}
}

func TestExecutionModeForUnrelatedOperationUnaffected(t *testing.T) {
	config := ERunConfig{Execution: ExecutionConfig{Modes: map[string]string{"aws-sts": "library"}}}
	if mode := ExecutionModeFor(config, "helm"); mode != ExecutionModeSubprocess {
		t.Fatalf("mode = %q, want %q", mode, ExecutionModeSubprocess)
	}
}

func TestExecutionModeReportListsKnownOperations(t *testing.T) {
	config := ERunConfig{Execution: ExecutionConfig{Modes: map[string]string{"aws-sts": "library"}}}
	statuses := ExecutionModeReport(config)
	if len(statuses) == 0 {
		t.Fatal("expected at least one reported operation")
	}
	found := false
	for _, status := range statuses {
		if status.Operation == "aws-sts" {
			found = true
			if status.Mode != ExecutionModeLibrary {
				t.Fatalf("aws-sts mode = %q, want %q", status.Mode, ExecutionModeLibrary)
			}
		}
	}
	if !found {
		t.Fatal("expected \"aws-sts\" in the execution mode report")
	}
}

func TestExecutionModeReportListsAWSWebIdentityTokenOperation(t *testing.T) {
	statuses := ExecutionModeReport(ERunConfig{})
	for _, status := range statuses {
		if status.Operation == "aws-sts-web-identity-token" {
			return
		}
	}
	t.Fatal("expected \"aws-sts-web-identity-token\" in the execution mode report")
}

func TestExecutionModeForAWSExportCredentialsDefaultsToSubprocess(t *testing.T) {
	if mode := ExecutionModeFor(ERunConfig{}, "aws-export-credentials"); mode != ExecutionModeSubprocess {
		t.Fatalf("mode = %q, want %q", mode, ExecutionModeSubprocess)
	}
}

func TestExecutionModeReportListsAWSExportCredentialsOperation(t *testing.T) {
	statuses := ExecutionModeReport(ERunConfig{})
	for _, status := range statuses {
		if status.Operation == "aws-export-credentials" {
			return
		}
	}
	t.Fatal("expected \"aws-export-credentials\" in the execution mode report")
}
