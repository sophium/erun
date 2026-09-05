package sshknownhosts

import (
	"errors"
	"strings"
	"testing"
)

const realEd25519Line = "[127.0.0.1]:17122 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIQVXbogPCLGY4kU3ZTIZgOX6uK5wKJn1nzTQ1E0uYm+1"

func TestParseHostKeyScanOutput_ReturnsOnlyTheRealKey(t *testing.T) {
	stdout := strings.Join([]string{
		"# 127.0.0.1:17122 SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13.18",
		realEd25519Line,
	}, "\n")
	stderr := "unsupported KEX method sntrup761x25519-sha512@openssh.com"

	lines, err := parseHostKeyScanOutput(stdout, stderr, nil)
	if err != nil {
		t.Fatalf("parseHostKeyScanOutput returned error: %v", err)
	}
	if len(lines) != 1 || lines[0] != realEd25519Line {
		t.Fatalf("expected only the real key line, got %v", lines)
	}
}

func TestParseHostKeyScanOutput_DiagnosticOnlyFailsRatherThanReturningLines(t *testing.T) {
	// A merged-stream regression would put this diagnostic on stdout; guard against
	// that by asserting the failure case even when it lands where a key would.
	stdout := "erun-validationagent-review unsupported KEX method sntrup761x25519-sha512@openssh.com"

	lines, err := parseHostKeyScanOutput(stdout, "", errors.New("exit status 1"))
	if err == nil {
		t.Fatalf("expected an error, got lines %v", lines)
	}
	if lines != nil {
		t.Fatalf("expected no lines on failure, got %v", lines)
	}
}

func TestParseHostKeyScanOutput_DiagnosticOnStderrOnlyFails(t *testing.T) {
	lines, err := parseHostKeyScanOutput("", "unsupported KEX method sntrup761x25519-sha512@openssh.com", errors.New("exit status 1"))
	if err == nil {
		t.Fatalf("expected an error, got lines %v", lines)
	}
	if lines != nil {
		t.Fatalf("expected no lines on failure, got %v", lines)
	}
	if !strings.Contains(err.Error(), "unsupported KEX method") {
		t.Fatalf("expected error to carry the diagnostic detail, got %q", err.Error())
	}
}

func TestParseHostKeyScanOutput_EmptyOutputFails(t *testing.T) {
	lines, err := parseHostKeyScanOutput("", "", nil)
	if err == nil {
		t.Fatalf("expected an error, got lines %v", lines)
	}
	if lines != nil {
		t.Fatalf("expected no lines on failure, got %v", lines)
	}
}

func TestIsHostKeyLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
	}{
		{"real ed25519 key", realEd25519Line, true},
		{"real ecdsa key", "host ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTY=", true},
		{"diagnostic with alias token", "erun-validationagent-review unsupported KEX method sntrup761x25519-sha512@openssh.com", false},
		{"diagnostic with host token", "[127.0.0.1]:17122 unsupported KEX method sntrup761x25519-sha512@openssh.com", false},
		{"too few fields", "host ssh-ed25519", false},
		{"blank", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isHostKeyLine(tc.line); got != tc.want {
				t.Fatalf("isHostKeyLine(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

func TestUpsertKnownHostsContent_RepairsAPreviouslyCorruptedEntry(t *testing.T) {
	alias := "erun-validationagent-review"
	hostToken := "[127.0.0.1]:17122"
	existing := strings.Join([]string{
		alias + " unsupported KEX method sntrup761x25519-sha512@openssh.com",
		hostToken + " unsupported KEX method sntrup761x25519-sha512@openssh.com",
		"other-alias ssh-ed25519 AAAAunrelated",
	}, "\n") + "\n"

	updated := upsertKnownHostsContent(existing, alias, hostToken, []string{realEd25519Line})

	if strings.Contains(updated, "unsupported KEX method") {
		t.Fatalf("expected the corrupted lines to be repaired, got:\n%s", updated)
	}
	if !strings.Contains(updated, "other-alias ssh-ed25519 AAAAunrelated") {
		t.Fatalf("expected the unrelated entry to survive, got:\n%s", updated)
	}
	if !strings.Contains(updated, alias+" ssh-ed25519") || !strings.Contains(updated, hostToken+" ssh-ed25519") {
		t.Fatalf("expected the alias and host token to be re-recorded with the real key, got:\n%s", updated)
	}
}
