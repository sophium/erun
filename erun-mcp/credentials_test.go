package erunmcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAndSerializeAWSCredentialsFileRoundTrip(t *testing.T) {
	input := []byte(`# managed by user
[default]
aws_access_key_id = AKIA-USER
aws_secret_access_key = user-secret

[erun-host]
aws_access_key_id = OLD
aws_secret_access_key = old-secret
aws_session_token = old-token
`)
	profiles := parseAWSCredentialsFile(input)
	if len(profiles) != 2 || profiles[0].Name != "default" || profiles[1].Name != "erun-host" {
		t.Fatalf("expected default + erun-host profiles, got %+v", profiles)
	}
	if len(profiles[0].Entries) != 2 || profiles[0].Entries[0].Value != "AKIA-USER" {
		t.Fatalf("unexpected default profile entries: %+v", profiles[0].Entries)
	}
	out := serializeAWSCredentialsFile(profiles)
	again := parseAWSCredentialsFile(out)
	if len(again) != 2 || again[1].Name != "erun-host" || again[1].Entries[2].Value != "old-token" {
		t.Fatalf("round-trip lost data: %s", string(out))
	}
}

func TestSetAWSCredentialProfileReplacesExisting(t *testing.T) {
	profiles := []awsCredentialProfile{
		{Name: "default", Entries: []awsCredentialEntry{{Key: "aws_access_key_id", Value: "AKIA-USER"}}},
		{Name: "erun-host", Entries: []awsCredentialEntry{{Key: "aws_access_key_id", Value: "OLD"}}},
	}
	updated := setAWSCredentialProfile(profiles, "erun-host", []awsCredentialEntry{
		{Key: "aws_access_key_id", Value: "NEW"},
		{Key: "aws_secret_access_key", Value: "new-secret"},
	})
	if len(updated) != 2 || updated[1].Name != "erun-host" || updated[1].Entries[0].Value != "NEW" {
		t.Fatalf("expected erun-host overwritten in place, got %+v", updated)
	}
	if updated[0].Entries[0].Value != "AKIA-USER" {
		t.Fatalf("default profile was disturbed: %+v", updated[0])
	}
}

func TestRemoveAWSCredentialProfile(t *testing.T) {
	profiles := []awsCredentialProfile{
		{Name: "default", Entries: []awsCredentialEntry{{Key: "aws_access_key_id", Value: "AKIA-USER"}}},
		{Name: "erun-host", Entries: []awsCredentialEntry{{Key: "aws_access_key_id", Value: "OLD"}}},
	}
	updated, removed := removeAWSCredentialProfile(profiles, "erun-host")
	if !removed || len(updated) != 1 || updated[0].Name != "default" {
		t.Fatalf("expected erun-host removed and default retained, got %+v removed=%v", updated, removed)
	}
}

func TestCloudInjectAWSCredentialsMergesIntoExistingFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	credsPath := filepath.Join(home, ".aws", "credentials")
	if err := os.MkdirAll(filepath.Dir(credsPath), 0o700); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	existing := []byte("[default]\naws_access_key_id = AKIA-USER\naws_secret_access_key = user-secret\n")
	if err := os.WriteFile(credsPath, existing, 0o600); err != nil {
		t.Fatalf("write existing failed: %v", err)
	}

	tool := cloudInjectAWSCredentialsTool()
	_, result, err := tool(context.Background(), nil, InjectAWSCredentialsInput{
		AccessKeyID:     "ASIA-HOST",
		SecretAccessKey: "host-secret",
		SessionToken:    "host-token",
		Expiration:      "2026-05-19T18:30:00Z",
	})
	if err != nil {
		t.Fatalf("inject failed: %v", err)
	}
	if result.Profile != "erun-host" || result.Path != credsPath {
		t.Fatalf("unexpected result: %+v", result)
	}
	data, err := os.ReadFile(credsPath)
	if err != nil {
		t.Fatalf("read back failed: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"[default]",
		"aws_access_key_id = AKIA-USER",
		"[erun-host]",
		"aws_access_key_id = ASIA-HOST",
		"aws_session_token = host-token",
		"x_erun_expiration = 2026-05-19T18:30:00Z",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected credentials file to contain %q, got:\n%s", want, content)
		}
	}
	stat, err := os.Stat(credsPath)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if mode := stat.Mode().Perm(); mode != 0o600 {
		t.Fatalf("expected file mode 0600, got %o", mode)
	}
}

func TestCloudClearAWSCredentialsRemovesProfileAndKeepsOthers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	credsPath := filepath.Join(home, ".aws", "credentials")
	if err := os.MkdirAll(filepath.Dir(credsPath), 0o700); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	existing := []byte("[default]\naws_access_key_id = AKIA-USER\n\n[erun-host]\naws_access_key_id = ASIA-HOST\naws_secret_access_key = host-secret\n")
	if err := os.WriteFile(credsPath, existing, 0o600); err != nil {
		t.Fatalf("write existing failed: %v", err)
	}

	tool := cloudClearAWSCredentialsTool()
	_, result, err := tool(context.Background(), nil, ClearAWSCredentialsInput{})
	if err != nil {
		t.Fatalf("clear failed: %v", err)
	}
	if !result.Removed || result.Profile != "erun-host" {
		t.Fatalf("expected erun-host to be reported as removed, got %+v", result)
	}
	data, err := os.ReadFile(credsPath)
	if err != nil {
		t.Fatalf("read back failed: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "[default]") || strings.Contains(content, "[erun-host]") {
		t.Fatalf("expected default kept and erun-host removed, got:\n%s", content)
	}
}

func TestCloudClearAWSCredentialsDeletesFileWhenLastProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	credsPath := filepath.Join(home, ".aws", "credentials")
	if err := os.MkdirAll(filepath.Dir(credsPath), 0o700); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	existing := []byte("[erun-host]\naws_access_key_id = ASIA-HOST\naws_secret_access_key = host-secret\n")
	if err := os.WriteFile(credsPath, existing, 0o600); err != nil {
		t.Fatalf("write existing failed: %v", err)
	}

	tool := cloudClearAWSCredentialsTool()
	_, _, err := tool(context.Background(), nil, ClearAWSCredentialsInput{})
	if err != nil {
		t.Fatalf("clear failed: %v", err)
	}
	if _, err := os.Stat(credsPath); !os.IsNotExist(err) {
		t.Fatalf("expected credentials file removed when last profile cleared, stat err=%v", err)
	}
}

func TestCloudInjectAWSCredentialsRequiresKeyAndSecret(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	tool := cloudInjectAWSCredentialsTool()
	if _, _, err := tool(context.Background(), nil, InjectAWSCredentialsInput{AccessKeyID: "ASIA"}); err == nil {
		t.Fatal("expected missing secret access key error")
	}
}
