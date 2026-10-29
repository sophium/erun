package main

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
	"github.com/sophium/erun/internal"
)

// Exit codes 1, 127, and 128 must stay distinct.
func TestExitCodeForKeepsTheOutcomesDistinct(t *testing.T) {
	if got := exitCodeFor(errors.New("boom")); got != 1 {
		t.Fatalf("ordinary failure exit %d, want 1", got)
	}

	aliasErr := fmt.Errorf("multiple erun platform cloud provider aliases are configured; pass --erun-alias to choose one: %w", providerconfig.ErrPlatformAliasUnusable)
	if got := exitCodeFor(aliasErr); got != platformAliasUnusableExitCode {
		t.Fatalf("unresolvable alias exit %d, want %d", got, platformAliasUnusableExitCode)
	}

	refusal := internal.WithExitCode(model.TenantSelectionUnavailableError{Tenants: []string{"erun", "frs"}}, 128)
	if got := exitCodeFor(refusal); got != 128 {
		t.Fatalf("tenant refusal exit %d, want 128", got)
	}
	if platformAliasUnusableExitCode == 128 {
		t.Fatal("the tenant refusal and the alias condition must not share a code")
	}
}

func TestRunDispatchesToClaudeOnlyForTheClaudeLink(t *testing.T) {
	var claudeArgs []string
	claudeCalls, erunCalls := 0, 0
	restoreClaude, restoreERun := execClaude, executeERun
	t.Cleanup(func() { execClaude, executeERun = restoreClaude, restoreERun })
	execClaude = func(args []string) error {
		claudeCalls++
		claudeArgs = args
		return nil
	}
	executeERun = func() error {
		erunCalls++
		return nil
	}

	if code := run([]string{"/usr/local/bin/claude", "--model", "opus"}); code != 0 || claudeCalls != 1 || erunCalls != 0 {
		t.Fatalf("claude link: code %d, claude %d, erun %d", code, claudeCalls, erunCalls)
	}
	if !reflect.DeepEqual(claudeArgs, []string{"--model", "opus"}) {
		t.Fatalf("claude args %q", claudeArgs)
	}
	for _, argv0 := range []string{"erun", "/usr/local/bin/erun", "claude-real", "./erun"} {
		claudeCalls, erunCalls = 0, 0
		if code := run([]string{argv0, "version"}); code != 0 || claudeCalls != 0 || erunCalls != 1 {
			t.Fatalf("%s: code %d, claude %d, erun %d", argv0, code, claudeCalls, erunCalls)
		}
	}
}

func TestRunClaudeReportsAFailedHandOver(t *testing.T) {
	restoreClaude, restoreStderr := execClaude, claudeStderr
	t.Cleanup(func() { execClaude, claudeStderr = restoreClaude, restoreStderr })
	var stderr bytes.Buffer
	claudeStderr = &stderr
	execClaude = func([]string) error { return errors.New("claude: Claude Code is not installed") }
	if code := run([]string{"claude"}); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "Claude Code is not installed") {
		t.Fatalf("stderr %q", stderr.String())
	}
}
