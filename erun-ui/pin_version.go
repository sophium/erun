package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// The desktop shells `erun pin` so CLI, MCP and UI share one re-pin engine.

// uiPinPlan is a resolved pin plan, with per-site current and target versions.
type uiPinPlan struct {
	Tenant      string      `json:"tenant"`
	Environment string      `json:"environment"`
	Target      string      `json:"target"`
	Previous    string      `json:"previous,omitempty"`
	Sites       []uiPinSite `json:"sites"`
	// Skipped lists references deliberately left on another release line.
	Skipped   []string         `json:"skipped,omitempty"`
	Changed   int              `json:"changed"`
	Aligned   bool             `json:"aligned"`
	Available []string         `json:"available,omitempty"`
	Notice    *uiVersionNotice `json:"notice,omitempty"`
}

type uiPinSite struct {
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	Current string `json:"current"`
	Target  string `json:"target"`
	Aligned bool   `json:"aligned"`
}

// PreviewPinVersion resolves a re-pin without writing; empty target means latest stable.
func (a *App) PreviewPinVersion(selection uiSelection, target string) (uiPinPlan, error) {
	return a.runPinCommand(selection, target, true, false)
}

// ApplyPinVersion performs the re-pin and reports what moved.
func (a *App) ApplyPinVersion(selection uiSelection, target string) (uiPinPlan, error) {
	return a.runPinCommand(selection, target, false, false)
}

// RevertPinVersion pins back to the version recorded before the last re-pin.
func (a *App) RevertPinVersion(selection uiSelection) (uiPinPlan, error) {
	return a.runPinCommand(selection, "", false, true)
}

func (a *App) runPinCommand(selection uiSelection, target string, preview, revert bool) (uiPinPlan, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("run pin command", selection.Tenant, selection.Environment); err != nil {
		return uiPinPlan{}, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	args := []string{"pin", selection.Tenant, selection.Environment, "--output", "json"}
	if preview {
		args = append(args, "--dry-run")
	}
	switch {
	case revert:
		args = append(args, "--revert")
	case strings.TrimSpace(target) != "":
		args = append(args, "--version", strings.TrimSpace(target))
	}

	plan, err := runPinCLI(ctx, a.deps.resolveCLIPath(), args)
	if err != nil {
		return uiPinPlan{}, err
	}
	plan.Tenant, plan.Environment = selection.Tenant, selection.Environment
	return plan, nil
}

// runPinCLI runs the CLI: JSON on stdout, human trace on stderr.
func runPinCLI(ctx context.Context, cliPath string, args []string) (uiPinPlan, error) {
	cmd := exec.CommandContext(ctx, cliPath, args...)
	core.HideConsoleWindow(cmd)
	core.BoundCommandWait(cmd)
	cmd.Env = os.Environ()

	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return uiPinPlan{}, fmt.Errorf("%s", lastMeaningfulLine(detail))
		}
		return uiPinPlan{}, err
	}

	var resolved model.PinPlan
	if unmarshalErr := json.Unmarshal(stdout, &resolved); unmarshalErr != nil {
		return uiPinPlan{}, fmt.Errorf("read the pin result: %w", unmarshalErr)
	}
	return toUIPinPlan(resolved), nil
}

// lastMeaningfulLine returns the last non-audit line of a trace.
func lastMeaningfulLine(detail string) string {
	lines := strings.Split(detail, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" && !strings.HasPrefix(line, "audit:") {
			return line
		}
	}
	return strings.TrimSpace(detail)
}

func toUIPinPlan(plan model.PinPlan) uiPinPlan {
	out := uiPinPlan{
		Tenant:      plan.Tenant,
		Environment: plan.Environment,
		Target:      plan.Target,
		Previous:    plan.Previous,
		Sites:       make([]uiPinSite, 0, len(plan.Sites)),
		Skipped:     plan.Skipped,
	}
	for _, site := range plan.Sites {
		out.Sites = append(out.Sites, uiPinSite{
			Kind:    string(site.Kind),
			Label:   pinSiteLabel(site),
			Current: site.Current,
			Target:  site.Target,
			Aligned: site.Aligned(),
		})
		if !site.Aligned() {
			out.Changed++
		}
	}
	out.Aligned = out.Changed == 0
	return out
}

// pinSiteLabel names a site by its repo path.
func pinSiteLabel(site model.PinSite) string {
	path := strings.TrimSpace(site.Path)
	detail := strings.TrimSpace(site.Detail)
	switch {
	case path == "":
		return detail
	case detail == "" || strings.Contains(detail, path):
		return path
	default:
		return path + " (" + detail + ")"
	}
}

// ListPinnableVersions lists the published versions the environment can pin to.
func (a *App) ListPinnableVersions(selection uiSelection) ([]string, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("list pinnable versions", selection.Tenant, selection.Environment); err != nil {
		return nil, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, a.deps.resolveCLIPath(), "pin", selection.Tenant, selection.Environment, "--list", "--output", "json")
	core.HideConsoleWindow(cmd)
	core.BoundCommandWait(cmd)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return nil, fmt.Errorf("%s", lastMeaningfulLine(detail))
		}
		return nil, err
	}
	var versions model.RuntimeRegistryVersions
	if unmarshalErr := json.Unmarshal(stdout, &versions); unmarshalErr != nil {
		return nil, fmt.Errorf("read the published versions: %w", unmarshalErr)
	}
	return versions.Tags, nil
}

// uiPinRepoCheckoutStatus reports whether `erun pin` can resolve a local checkout.
type uiPinRepoCheckoutStatus struct {
	Resolvable bool   `json:"resolvable"`
	Reason     string `json:"reason,omitempty"`
}

// PinRepoCheckoutStatus mirrors the CLI's resolvePinProjectRoot: a remote-worktree env
// needs a same-tenant sibling with a local checkout.
func (a *App) PinRepoCheckoutStatus(selection uiSelection) (uiPinRepoCheckoutStatus, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("resolve pin repo checkout status", selection.Tenant, selection.Environment); err != nil {
		return uiPinRepoCheckoutStatus{}, err
	}
	target, _, err := a.deps.store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return uiPinRepoCheckoutStatus{}, err
	}
	if !target.RemoteWorktree() {
		return uiPinRepoCheckoutStatus{Resolvable: true}, nil
	}
	siblings, err := a.deps.store.ListEnvConfigs(selection.Tenant)
	if err != nil {
		return uiPinRepoCheckoutStatus{}, err
	}
	if _, ok := config.TenantLocalCheckoutRoot(siblings); ok {
		return uiPinRepoCheckoutStatus{Resolvable: true}, nil
	}
	return uiPinRepoCheckoutStatus{
		Resolvable: false,
		Reason: fmt.Sprintf(
			"%s/%s has no local checkout of its repo on this machine, and no other %s environment does either. "+
				"Pin rewrites files in that checkout — check out the tenant repo somewhere on this machine, or run `erun pin` from a machine that already has it.",
			selection.Tenant, selection.Environment, selection.Tenant),
	}, nil
}
