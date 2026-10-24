package shared

import (
	"errors"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
)

// openTenantResolutionStore is a minimal OpenStore for exercising
// resolveOpenTenant/resolveOpenEnvironment without a real config root.
type openTenantResolutionStore struct {
	defaultTenant      string
	defaultEnvironment string
}

func (s openTenantResolutionStore) LoadERunConfig() (config.ERunConfig, string, error) {
	if s.defaultTenant == "" {
		return config.ERunConfig{}, "", config.ErrNotInitialized
	}
	return config.ERunConfig{DefaultTenant: s.defaultTenant}, "", nil
}

func (s openTenantResolutionStore) LoadTenantConfig(name string) (config.TenantConfig, string, error) {
	return config.TenantConfig{Name: name, DefaultEnvironment: s.defaultEnvironment}, "", nil
}

func (s openTenantResolutionStore) LoadEnvConfig(tenant, environment string) (config.EnvConfig, string, error) {
	return config.EnvConfig{Name: environment}, "", nil
}

// noProjectRoot simulates running outside any tenant's project checkout, so
// resolveOpenTenant's cwd fallback never resolves a tenant.
func noProjectRoot() (string, string, error) {
	return "", "", config.ErrNotInGitRepository
}

func TestResolveOpenTenantInferenceForbidden(t *testing.T) {
	store := openTenantResolutionStore{}
	_, err := ResolveOpenTenant(store, noProjectRoot, OpenParams{UseDefaultTenant: false})
	if err == nil {
		t.Fatal("expected an error when no tenant is given and inference is not permitted")
	}
	if !errors.Is(err, ErrOpenTenantNotProvided) {
		t.Fatalf("expected ErrOpenTenantNotProvided, got: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "open") {
		t.Fatalf("error must name the operation (open): %q", msg)
	}
	if !strings.Contains(msg, "pass a tenant explicitly") {
		t.Fatalf("error must name its recovery (pass a tenant explicitly): %q", msg)
	}
	if errors.Is(err, ErrDefaultTenantNotConfigured) {
		t.Fatalf("inference-forbidden case must not be mistaken for the inference-permitted-but-unresolved case: %q", msg)
	}
}

// TestResolveOpenTenantNamesTheCommandThatActuallyFailed is the regression for
// the failure naming a fixed operation: it must name the operation the
// operator ran, and offer that operation's own recovery, rather than "open"'s.
func TestResolveOpenTenantNamesTheCommandThatActuallyFailed(t *testing.T) {
	store := openTenantResolutionStore{}
	tests := []struct {
		command            string
		scopesTenantByFlag bool
	}{
		{"erun outputs list", true},
		// build takes the tenant positionally, so its recovery must name the
		// command without asserting a --tenant flag it does not have.
		{"erun build", false},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			_, err := ResolveOpenTenant(store, noProjectRoot, OpenParams{
				UseDefaultTenant:          true,
				Command:                   tt.command,
				CommandScopesTenantByFlag: tt.scopesTenantByFlag,
			})
			if !errors.Is(err, ErrDefaultTenantNotConfigured) {
				t.Fatalf("expected ErrDefaultTenantNotConfigured, got: %v", err)
			}
			msg := err.Error()
			if strings.Contains(msg, "open could not infer") {
				t.Fatalf("failure must not blame open for %s: %q", tt.command, msg)
			}
			if !strings.Contains(msg, tt.command) {
				t.Fatalf("failure must name the command that ran (%s): %q", tt.command, msg)
			}
			if !strings.Contains(msg, "could not be inferred") {
				t.Fatalf("failure must still disclose that working-directory inference failed: %q", msg)
			}
			if !strings.Contains(msg, "set-default-tenant") {
				t.Fatalf("failure must still offer the set-a-default remedy: %q", msg)
			}
			flagged := strings.Contains(msg, tt.command+" --tenant <name>")
			if flagged != tt.scopesTenantByFlag {
				t.Fatalf("recovery must offer --tenant for %s only if that flag scopes it (want %v): %q",
					tt.command, tt.scopesTenantByFlag, msg)
			}
		})
	}
}

// TestResolveOpenTenantRecoveryFollowsANewlyAdoptingCommand locks the property
// rather than the strings: a command that adopts this resolution path with its
// own invocation gets its own recovery named, with no edit to the resolver.
func TestResolveOpenTenantRecoveryFollowsANewlyAdoptingCommand(t *testing.T) {
	store := openTenantResolutionStore{}
	const adopting = "erun summarize"
	_, err := ResolveOpenTenant(store, noProjectRoot, OpenParams{
		UseDefaultTenant:          true,
		Command:                   adopting,
		CommandScopesTenantByFlag: true,
	})
	if err == nil {
		t.Fatal("expected an error when nothing could be inferred")
	}
	msg := err.Error()
	if !strings.Contains(msg, "`"+adopting+" --tenant <name>`") {
		t.Fatalf("a newly adopting command must get its own recovery named: %q", msg)
	}
	for _, other := range []string{"open", "usage", "deploy"} {
		if strings.Contains(msg, other) {
			t.Fatalf("a newly adopting command must not inherit %q's name: %q", other, msg)
		}
	}
}

// TestResolveOpenTenantCommandWithNonScopingTenantFlagOmitsFlagRemedy covers
// the other half of naming the real next step: a command whose --tenant means
// something else (list's selects version-drift reporting) must not be told to
// pass --tenant as the tenant fix, since that flag would not do what the
// operator wants.
func TestResolveOpenTenantCommandWithNonScopingTenantFlagOmitsFlagRemedy(t *testing.T) {
	store := openTenantResolutionStore{}
	_, err := ResolveOpenTenant(store, noProjectRoot, OpenParams{
		UseDefaultTenant:          true,
		Command:                   "erun list",
		CommandScopesTenantByFlag: false,
	})
	if err == nil {
		t.Fatal("expected an error when nothing could be inferred")
	}
	msg := err.Error()
	if strings.Contains(msg, "erun list --tenant") {
		t.Fatalf("a non-scoping --tenant must not be offered as the tenant remedy: %q", msg)
	}
	if !strings.Contains(msg, "erun list") {
		t.Fatalf("failure must still name the command that ran: %q", msg)
	}
}
