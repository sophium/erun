package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// The claim label an orchestrator addresses an issue by. erun-orchestrate's
// claim protocol opens with `gh issue edit --add-label wip:<id>`, and GitHub
// rejects a label the repository does not have — so an id whose label was never
// minted cannot make the claim at all. A session in that position either skips
// the claim silently, which is the double-planning the convention exists to
// prevent, or improvises a remedy with nothing telling it to.
//
// Minting it here puts the label in the same provisioning path as the role file
// and the SessionStart hook: one id, one label, created once and then left
// alone.
const (
	// orchestratorClaimLabelRepo is named on every call rather than inferred
	// from the directory the command runs in. An orchestrator session's working
	// directory is the shared orchestrators root, which is not a git checkout,
	// so `gh` there cannot resolve a repository and fails with "fatal: not a
	// git repository" — the repository has to be part of the command.
	//
	// It is the repository every sibling claim label lives in, including the
	// ones whose ids name another product: a claim label is what an erun
	// orchestrator claims erun issues with, so it belongs to erun's tracker
	// rather than to the tenant repository an id happens to be named after.
	orchestratorClaimLabelRepo = "sophium/erun"
	// orchestratorClaimLabelColor matches every sibling claim label already on
	// the repository.
	orchestratorClaimLabelColor = "FBCA04"
	// orchestratorClaimLabelListLimit bounds the existence probe. `gh`'s own
	// default page is smaller than a repository's label set routinely is, and a
	// probe that truncates reads an existing label as missing.
	orchestratorClaimLabelListLimit = "100"
)

// orchestratorClaimLabelTimeout bounds the whole step. It runs on the
// orchestrator launch path, so a GitHub that never answers must delay a launch
// by nothing at all.
const orchestratorClaimLabelTimeout = 10 * time.Second

// orchestratorClaimLabelName is the label id claims issues with, empty when id
// names no orchestrator at all (a transient session), and an error when id
// cannot name a label.
//
// Ids erun mints are already slugified, so this is the guard for a hand-edited
// config.yaml id: whitespace in a label name would make the existence probe
// below match on something other than the whole line it read.
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

// orchestratorClaimLabelDescription is the description every sibling carries,
// naming this id as the holder.
func orchestratorClaimLabelDescription(id string) string {
	return "Claimed by orchestrator " + strings.TrimSpace(id) +
		". Do not start this issue; see AGENTS.md / erun-orchestrate."
}

// ensureOrchestratorClaimLabel makes sure id's claim label exists on the
// repository its claim protocol targets, and leaves a label that is already
// there strictly alone: that label is what a live holder is claiming under, so
// rewriting its colour or description would be the fight over an existing claim
// the convention forbids.
//
// Called on every launch rather than only at creation, and for the same reason
// the role file is seeded here too: an id provisioned before this step existed
// gets its label the first time it launches under a build that has one. It
// re-asks rather than remembering the answer locally, because the label lives
// on the repository: a local record of it would be a claim about remote state
// read off this host's disk, which is what the shared contract forbids for
// everything else about an orchestrator and would be no truer here.
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
