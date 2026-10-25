package activity

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
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// The desktop shells `erun pin` so CLI, MCP and UI share one re-pin engine.

// PinPlan is a resolved pin plan, with per-site current and target versions.
type PinPlan struct {
	Tenant      string    `json:"tenant"`
	Environment string    `json:"environment"`
	Target      string    `json:"target"`
	Previous    string    `json:"previous,omitempty"`
	Sites       []PinSite `json:"sites"`
	// Skipped lists references deliberately left on another release line.
	Skipped   []string               `json:"skipped,omitempty"`
	Changed   int                    `json:"changed"`
	Aligned   bool                   `json:"aligned"`
	Available []string               `json:"available,omitempty"`
	Notice    *uimodel.VersionNotice `json:"notice,omitempty"`
}

type PinSite struct {
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	Current string `json:"current"`
	Target  string `json:"target"`
	Aligned bool   `json:"aligned"`
}

// PreviewPinVersion resolves a re-pin without writing; empty target means latest stable.
func (s *Service) PreviewPinVersion(selection uimodel.Selection, target string) (PinPlan, error) {
	return s.runPinCommand(selection, target, true, false)
}

// ApplyPinVersion performs the re-pin and reports what moved.
func (s *Service) ApplyPinVersion(selection uimodel.Selection, target string) (PinPlan, error) {
	return s.runPinCommand(selection, target, false, false)
}

// RevertPinVersion pins back to the version recorded before the last re-pin.
func (s *Service) RevertPinVersion(selection uimodel.Selection) (PinPlan, error) {
	return s.runPinCommand(selection, "", false, true)
}

func (s *Service) runPinCommand(selection uimodel.Selection, target string, preview, revert bool) (PinPlan, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("run pin command", selection.Tenant, selection.Environment); err != nil {
		return PinPlan{}, err
	}
	ctx := s.d.Context()

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

	plan, err := runPinCLI(ctx, s.d.ResolveCLIPath(), args)
	if err != nil {
		return PinPlan{}, err
	}
	plan.Tenant, plan.Environment = selection.Tenant, selection.Environment
	return plan, nil
}

// runPinCLI runs the CLI: JSON on stdout, human trace on stderr.
func runPinCLI(ctx context.Context, cliPath string, args []string) (PinPlan, error) {
	cmd := exec.CommandContext(ctx, cliPath, args...)
	core.HideConsoleWindow(cmd)
	core.BoundCommandWait(cmd)
	cmd.Env = os.Environ()

	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return PinPlan{}, fmt.Errorf("%s", lastMeaningfulLine(detail))
		}
		return PinPlan{}, err
	}

	var resolved model.PinPlan
	if unmarshalErr := json.Unmarshal(stdout, &resolved); unmarshalErr != nil {
		return PinPlan{}, fmt.Errorf("read the pin result: %w", unmarshalErr)
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

func toUIPinPlan(plan model.PinPlan) PinPlan {
	out := PinPlan{
		Tenant:      plan.Tenant,
		Environment: plan.Environment,
		Target:      plan.Target,
		Previous:    plan.Previous,
		Sites:       make([]PinSite, 0, len(plan.Sites)),
		Skipped:     plan.Skipped,
	}
	for _, site := range plan.Sites {
		out.Sites = append(out.Sites, PinSite{
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
func (s *Service) ListPinnableVersions(selection uimodel.Selection) ([]string, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("list pinnable versions", selection.Tenant, selection.Environment); err != nil {
		return nil, err
	}
	ctx := s.d.Context()
	cmd := exec.CommandContext(ctx, s.d.ResolveCLIPath(), "pin", selection.Tenant, selection.Environment, "--list", "--output", "json")
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

// PinCheckoutStatus reports whether `erun pin` can resolve a local checkout.
type PinCheckoutStatus struct {
	Resolvable bool   `json:"resolvable"`
	Reason     string `json:"reason,omitempty"`
}

// PinRepoCheckoutStatus mirrors the CLI's resolvePinProjectRoot: a remote-worktree env
// needs a same-tenant sibling with a local checkout.
func (s *Service) PinRepoCheckoutStatus(selection uimodel.Selection) (PinCheckoutStatus, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("resolve pin repo checkout status", selection.Tenant, selection.Environment); err != nil {
		return PinCheckoutStatus{}, err
	}
	target, _, err := s.d.Store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return PinCheckoutStatus{}, err
	}
	if !target.RemoteWorktree() {
		return PinCheckoutStatus{Resolvable: true}, nil
	}
	siblings, err := s.d.Store.ListEnvConfigs(selection.Tenant)
	if err != nil {
		return PinCheckoutStatus{}, err
	}
	if _, ok := config.TenantLocalCheckoutRoot(siblings); ok {
		return PinCheckoutStatus{Resolvable: true}, nil
	}
	return PinCheckoutStatus{
		Resolvable: false,
		Reason: fmt.Sprintf(
			"%s/%s has no local checkout of its repo on this machine, and no other %s environment does either. "+
				"Pin rewrites files in that checkout — check out the tenant repo somewhere on this machine, or run `erun pin` from a machine that already has it.",
			selection.Tenant, selection.Environment, selection.Tenant),
	}, nil
}
