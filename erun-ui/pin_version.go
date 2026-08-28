package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// Changing an environment's erun version means moving every place that version
// is recorded — the Terraform refs, each umbrella's erun chart dependencies, the
// build-env image tag, the env's own runtime version — together. The desktop
// composes `erun pin` for that rather than reimplementing it: the CLI, the MCP
// tool and this share one engine, so all three agree on what a re-pin touches.

// uiPinPlan is the desktop's read-model of a resolved plan. It carries the
// sites so the dialog can show current → target per reference, which is what
// makes the motion something an operator can agree to rather than trust.
type uiPinPlan struct {
	Tenant      string           `json:"tenant"`
	Environment string           `json:"environment"`
	Target      string           `json:"target"`
	Previous    string           `json:"previous,omitempty"`
	Sites       []uiPinSite      `json:"sites"`
	Changed     int              `json:"changed"`
	Aligned     bool             `json:"aligned"`
	Available   []string         `json:"available,omitempty"`
	Notice      *uiVersionNotice `json:"notice,omitempty"`
}

type uiPinSite struct {
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	Current string `json:"current"`
	Target  string `json:"target"`
	Aligned bool   `json:"aligned"`
}

// PreviewPinVersion resolves what re-pinning this environment to target would
// change, without writing. An empty target means the latest published stable.
func (a *App) PreviewPinVersion(selection uiSelection, target string) (uiPinPlan, error) {
	return a.runPinCommand(selection, target, true, false)
}

// ApplyPinVersion performs the re-pin and reports what moved.
func (a *App) ApplyPinVersion(selection uiSelection, target string) (uiPinPlan, error) {
	return a.runPinCommand(selection, target, false, false)
}

// RevertPinVersion pins back to the version recorded before the last re-pin, so
// trying a version out is cheap to undo.
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

// runPinCLI shells the CLI and reads its structured result. The human trace goes
// to stderr and the JSON to stdout, so a failure can be reported with the
// command's own words rather than a generic one.
func runPinCLI(ctx context.Context, cliPath string, args []string) (uiPinPlan, error) {
	cmd := exec.CommandContext(ctx, cliPath, args...)
	eruncommon.HideConsoleWindow(cmd)
	eruncommon.BoundCommandWait(cmd)
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

	var resolved eruncommon.PinPlan
	if unmarshalErr := json.Unmarshal(stdout, &resolved); unmarshalErr != nil {
		return uiPinPlan{}, fmt.Errorf("read the pin result: %w", unmarshalErr)
	}
	return toUIPinPlan(resolved), nil
}

// lastMeaningfulLine picks the line an operator should read out of a trace that
// also carries audit lines, so the dialog shows the reason rather than the
// command's opening line.
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

func toUIPinPlan(plan eruncommon.PinPlan) uiPinPlan {
	out := uiPinPlan{
		Tenant:      plan.Tenant,
		Environment: plan.Environment,
		Target:      plan.Target,
		Previous:    plan.Previous,
		Sites:       make([]uiPinSite, 0, len(plan.Sites)),
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

// pinSiteLabel names the reference in the terms the repo uses, so a site in the
// dialog can be found on disk without translation.
func pinSiteLabel(site eruncommon.PinSite) string {
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

// ListPinnableVersions answers what the environment can be pinned to, so the
// picker offers real published versions rather than a free-text box.
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
	eruncommon.HideConsoleWindow(cmd)
	eruncommon.BoundCommandWait(cmd)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return nil, fmt.Errorf("%s", lastMeaningfulLine(detail))
		}
		return nil, err
	}
	var versions eruncommon.RuntimeRegistryVersions
	if unmarshalErr := json.Unmarshal(stdout, &versions); unmarshalErr != nil {
		return nil, fmt.Errorf("read the published versions: %w", unmarshalErr)
	}
	return versions.Tags, nil
}

// uiPinRepoCheckoutStatus answers, ahead of Preview/Apply, whether this
// environment's erun references have a resolvable local checkout on this
// machine. A sourceless runtime environment (no MountSource) has none of its
// own, and the dialog needs to know before it offers a plan `erun pin` can
// never resolve — rather than only discovering it once a Preview click fails.
type uiPinRepoCheckoutStatus struct {
	Resolvable bool   `json:"resolvable"`
	Reason     string `json:"reason,omitempty"`
}

// PinRepoCheckoutStatus mirrors resolvePinProjectRoot's own decision
// (erun-cli/cmd/pin.go) without running the CLI: an environment whose worktree
// lives on this machine always has one; a remote-worktree environment
// (remote-agent or runtime) needs a sibling environment of the same tenant
// that does, since every environment of a tenant shares one repo.
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
	if _, ok := eruncommon.TenantLocalCheckoutRoot(siblings); ok {
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
