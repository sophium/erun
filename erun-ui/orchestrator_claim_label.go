package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// GitHub rejects adding a label the repository lacks, so the orchestrator's
// wip:<id> claim label is minted at launch.
const (
	// The session runs in the non-git orchestrators root, so the repo is always
	// passed explicitly; claim labels live in erun's tracker whatever the id names.
	orchestratorClaimLabelRepo  = "sophium/erun"
	orchestratorClaimLabelColor = "FBCA04"
	// gh's default page is smaller than the label set; a truncated probe misreads
	// an existing label as missing.
	orchestratorClaimLabelListLimit = "100"
)

// orchestratorClaimLabelTimeout bounds the step; it runs on the launch path.
const orchestratorClaimLabelTimeout = 10 * time.Second

// orchestratorClaimLabelName returns "" for a transient session and rejects
// hand-edited ids with whitespace, which would break the whole-line probe.
func orchestratorClaimLabelName(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", nil
	}
	if strings.ContainsAny(id, " \t\r\n") {
		return "", fmt.Errorf("orchestrator id %q cannot name a claim label", id)
	}
	return "wip:" + id, nil
}

func orchestratorClaimLabelDescription(id string) string {
	return "Claimed by orchestrator " + strings.TrimSpace(id) +
		". Do not start this issue; see AGENTS.md / erun-orchestrate."
}

// ensureOrchestratorClaimLabel creates id's label if missing and never rewrites an
// existing one, which a live holder may be claiming under. It re-probes on every
// launch rather than caching remote state locally.
func (a *App) ensureOrchestratorClaimLabel(ctx context.Context, dir, id string) error {
	name, err := orchestratorClaimLabelName(id)
	if err != nil {
		return err
	}
	if name == "" {
		return nil // a transient session has no id and claims nothing
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, orchestratorClaimLabelTimeout)
	defer cancel()

	listed, err := a.deps.runOrchestratorLabelCommand(ctx, dir, "gh", "label", "list",
		"--repo", orchestratorClaimLabelRepo,
		"--limit", orchestratorClaimLabelListLimit,
		"--json", "name", "-q", ".[].name")
	if err != nil {
		return fmt.Errorf("list labels on %s: %w", orchestratorClaimLabelRepo, err)
	}
	for _, line := range strings.Split(listed, "\n") {
		if strings.TrimSpace(line) == name {
			return nil
		}
	}

	if _, err := a.deps.runOrchestratorLabelCommand(ctx, dir, "gh", "label", "create", name,
		"--repo", orchestratorClaimLabelRepo,
		"--color", orchestratorClaimLabelColor,
		"--description", orchestratorClaimLabelDescription(id)); err != nil {
		return fmt.Errorf("create label %s on %s: %w", name, orchestratorClaimLabelRepo, err)
	}
	return nil
}
