package cloud

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// runHostAWSCredentialsWriteScript runs the real pod-side script locally with payload on stdin.
func runHostAWSCredentialsWriteScript(t *testing.T, homeDir, payload string) error {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", hostAWSCredentialsWriteScript())
	cmd.Env = append(os.Environ(), "HOME="+homeDir)
	cmd.Stdin = strings.NewReader(payload)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("write script: %w: %s", err, out)
	}
	return nil
}

func hostAWSCredentialsTestPayload(sessionToken string) string {
	return renderHostAWSCredentialsProfile(CloudProviderCredentials{
		AccessKeyID:     "AKIAEXAMPLE",
		SecretAccessKey: "example-secret",
		SessionToken:    sessionToken,
		Expiration:      time.Date(2026, 9, 2, 15, 48, 22, 0, time.UTC),
	}, "eu-west-2")
}

func readHostAWSCredentialsFile(t *testing.T, homeDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(homeDir, ".aws", "credentials"))
	if err != nil {
		t.Fatalf("read credentials file: %v", err)
	}
	return string(data)
}

func TestHostAWSCredentialsWriteScriptConcurrentRefreshesNeverDuplicateProfile(t *testing.T) {
	homeDir := t.TempDir()
	const writers = 12
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(i int) {
			defer wg.Done()
			if err := runHostAWSCredentialsWriteScript(t, homeDir, hostAWSCredentialsTestPayload(fmt.Sprintf("session-token-%d", i))); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	content := readHostAWSCredentialsFile(t, homeDir)
	header := "[" + HostAWSCredentialsProfile + "]"
	if count := strings.Count(content, header); count != 1 {
		t.Fatalf("expected exactly one %q section after %d concurrent refreshes, got %d:\n%s", header, writers, count, content)
	}
	if strings.Count(content, "aws_access_key_id") != 1 {
		t.Fatalf("expected exactly one aws_access_key_id line, got a duplicated profile body:\n%s", content)
	}
}

func TestHostAWSCredentialsWriteScriptSequentialRefreshesReplaceProfile(t *testing.T) {
	homeDir := t.TempDir()
	if err := runHostAWSCredentialsWriteScript(t, homeDir, hostAWSCredentialsTestPayload("first-session-token")); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if err := runHostAWSCredentialsWriteScript(t, homeDir, hostAWSCredentialsTestPayload("second-session-token")); err != nil {
		t.Fatalf("second refresh: %v", err)
	}

	content := readHostAWSCredentialsFile(t, homeDir)
	header := "[" + HostAWSCredentialsProfile + "]"
	if count := strings.Count(content, header); count != 1 {
		t.Fatalf("expected exactly one %q section after two sequential refreshes, got %d:\n%s", header, count, content)
	}
	if strings.Contains(content, "first-session-token") {
		t.Fatalf("second refresh must replace the first profile, not leave it behind:\n%s", content)
	}
	if !strings.Contains(content, "second-session-token") {
		t.Fatalf("second refresh's own profile is missing:\n%s", content)
	}
}

func TestHostAWSCredentialsWriteScriptPreservesOtherProfiles(t *testing.T) {
	homeDir := t.TempDir()
	awsDir := filepath.Join(homeDir, ".aws")
	if err := os.MkdirAll(awsDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seed := "[other-profile]\naws_access_key_id = OTHERKEY\naws_secret_access_key = othersecret\n"
	if err := os.WriteFile(filepath.Join(awsDir, "credentials"), []byte(seed), 0o600); err != nil {
		t.Fatalf("seed credentials: %v", err)
	}

	if err := runHostAWSCredentialsWriteScript(t, homeDir, hostAWSCredentialsTestPayload("session-token")); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	content := readHostAWSCredentialsFile(t, homeDir)
	if !strings.Contains(content, "[other-profile]") || !strings.Contains(content, "OTHERKEY") {
		t.Fatalf("refresh must leave other profiles alone:\n%s", content)
	}
	if strings.Count(content, "["+HostAWSCredentialsProfile+"]") != 1 {
		t.Fatalf("expected exactly one erun-host section:\n%s", content)
	}
}

// A read-only .aws directory makes the write fail; the original file must be unchanged.
func TestHostAWSCredentialsWriteScriptFailedWriteLeavesOriginalProfileIntact(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root ignores directory permission bits, so the write would not fail")
	}
	homeDir := t.TempDir()
	awsDir := filepath.Join(homeDir, ".aws")
	if err := os.MkdirAll(awsDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	original := "[erun-host]\naws_access_key_id = ORIGINALKEY\naws_secret_access_key = originalsecret\nx_erun_expiration = 2026-09-02T15:48:22Z\n"
	credentialsPath := filepath.Join(awsDir, "credentials")
	if err := os.WriteFile(credentialsPath, []byte(original), 0o600); err != nil {
		t.Fatalf("seed credentials: %v", err)
	}
	if err := os.Chmod(awsDir, 0o500); err != nil {
		t.Fatalf("chmod .aws read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(awsDir, 0o700) })

	if err := runHostAWSCredentialsWriteScript(t, homeDir, hostAWSCredentialsTestPayload("session-token")); err == nil {
		t.Fatalf("expected the write to fail against a read-only .aws directory")
	}

	if err := os.Chmod(awsDir, 0o700); err != nil {
		t.Fatalf("restore .aws permissions: %v", err)
	}
	content := readHostAWSCredentialsFile(t, homeDir)
	if content != original {
		t.Fatalf("a failed write must leave the original profile untouched, got:\n%s", content)
	}
}
