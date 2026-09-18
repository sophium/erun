package main

import (
	"errors"
	"fmt"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/internal"
)

// The three outcomes a script that discards stderr has to tell apart: the run
// failed (1), the run cannot resolve a platform alias (127), and the run needed
// a choice only its caller can make (128). Collapsing any two of them is the
// ambiguity this contract exists to prevent.
func TestExitCodeForKeepsTheOutcomesDistinct(t *testing.T) {
	if got := exitCodeFor(errors.New("boom")); got != 1 {
		t.Fatalf("ordinary failure exit %d, want 1", got)
	}

	aliasErr := fmt.Errorf("multiple erun platform cloud provider aliases are configured; pass --erun-alias to choose one: %w", eruncommon.ErrPlatformAliasUnusable)
	if got := exitCodeFor(aliasErr); got != platformAliasUnusableExitCode {
		t.Fatalf("unresolvable alias exit %d, want %d", got, platformAliasUnusableExitCode)
	}

	refusal := internal.WithExitCode(eruncommon.TenantSelectionUnavailableError{Tenants: []string{"erun", "frs"}}, 128)
	if got := exitCodeFor(refusal); got != 128 {
		t.Fatalf("tenant refusal exit %d, want 128", got)
	}
	if platformAliasUnusableExitCode == 128 {
		t.Fatal("the tenant refusal and the alias condition must not share a code")
	}
}
