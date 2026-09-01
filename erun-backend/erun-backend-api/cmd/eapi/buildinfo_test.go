package main

import "testing"

func TestCurrentBuildVersionReportsAnUnstampedBuildAsDevNotEmpty(t *testing.T) {
	original := buildVersion
	defer func() { buildVersion = original }()

	buildVersion = ""
	if got := currentBuildVersion(); got != "dev" {
		t.Fatalf("currentBuildVersion() = %q, want %q for an unstamped build", got, "dev")
	}
}

func TestCurrentBuildVersionReportsTheStampedVersion(t *testing.T) {
	original := buildVersion
	defer func() { buildVersion = original }()

	buildVersion = "1.0.221"
	if got := currentBuildVersion(); got != "1.0.221" {
		t.Fatalf("currentBuildVersion() = %q, want %q", got, "1.0.221")
	}
}
