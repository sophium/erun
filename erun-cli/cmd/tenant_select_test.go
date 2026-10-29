package cmd

import (
	"errors"
	"fmt"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
	"github.com/sophium/erun/internal"
)

func TestBootstrapInitExitErrorKeepsTheOutcomesDistinct(t *testing.T) {
	unavailable := model.TenantSelectionUnavailableError{Tenants: []string{"erun", "frs"}}
	tagged := bootstrapInitExitError(unavailable)
	if got := internal.ExitCodeFor(tagged); got != tenantSelectionUnavailableExitCode {
		t.Fatalf("exit code %d, want %d", got, tenantSelectionUnavailableExitCode)
	}
	if errors.Is(tagged, providerconfig.ErrPlatformAliasUnusable) {
		t.Fatal("a tenant refusal must not report the platform-alias condition")
	}

	aliasErr := fmt.Errorf("multiple erun platform cloud provider aliases are configured; pass --erun-alias to choose one: %w", providerconfig.ErrPlatformAliasUnusable)
	passed := bootstrapInitExitError(aliasErr)
	if !errors.Is(passed, providerconfig.ErrPlatformAliasUnusable) {
		t.Fatal("an unresolvable platform alias must keep its own sentinel to report 127")
	}
	if errors.Is(passed, config.ErrNotInGitRepository) {
		t.Fatal("alias failure was reclassified")
	}
}

func TestTenantSelectionUnavailableExitCodeIsItsOwnContract(t *testing.T) {
	// Scripts branch on this code; it must differ from 1 and 127.
	if tenantSelectionUnavailableExitCode != 128 {
		t.Fatalf("tenantSelectionUnavailableExitCode = %d, want 128", tenantSelectionUnavailableExitCode)
	}
}

func TestBootstrapInitExitErrorStillReportsAMissingRepository(t *testing.T) {
	err := bootstrapInitExitError(fmt.Errorf("outer: %w", config.ErrNotInGitRepository))
	if !errors.Is(err, config.ErrNotInGitRepository) || !internal.IsReported(err) {
		t.Fatalf("missing-repository error lost its reported marking: %v", err)
	}
}
