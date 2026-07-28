package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// InvestigateFailure captures a failure report to a host file and spawns a
// fresh, prompt-seeded orchestrator to fix it or improve error reporting. The
// report is delivered as a file the agent reads — a large report doesn't survive
// being typed at a prompt — and the seed prompt references its path plus the
// standing instruction to file an erun issue for platform bugs. The orchestrator
// is transient (not persisted), grouped under the failed env's tenant, and runs
// in that env's host workspace when one is synced.
func (a *App) InvestigateFailure(report, tenant, environment string, cols, rows int) (orchestratorInfo, error) {
	report = strings.TrimSpace(report)
	if report == "" {
		return orchestratorInfo{}, fmt.Errorf("nothing to investigate: the failure report is empty")
	}
	path, err := stageInvestigationReport(report)
	if err != nil {
		return orchestratorInfo{}, err
	}
	prompt := fmt.Sprintf("A failure report is saved at %s. Read it, then plan to fix the issue, or improve error reporting so similar issues can be fixed faster. If this is an erun platform issue rather than a project issue, file a bug in the erun GitHub tracker (sophium/erun) using the erun-file-issue skill.", path)

	var envs []eruncommon.OrchestratorEnvConfig
	if t, env := strings.TrimSpace(tenant), strings.TrimSpace(environment); t != "" && env != "" {
		envs = []eruncommon.OrchestratorEnvConfig{
			{Tenant: t, Environment: env, Directory: a.investigateWorkingDir(t, env)},
		}
	}
	name := "Investigate"
	if env := strings.TrimSpace(environment); env != "" {
		name = "Investigate " + env
	}
	id := fmt.Sprintf("investigate-%d", time.Now().UnixNano())
	return a.spawnOrchestratorSession(id, name, envs, prompt, "", true, cols, rows)
}

// investigateWorkingDir resolves the env's host workspace to run the
// investigation in; "" (no synced workspace) falls back to the harness default.
func (a *App) investigateWorkingDir(tenant, environment string) string {
	if strings.TrimSpace(tenant) == "" || strings.TrimSpace(environment) == "" {
		return ""
	}
	_, path, err := a.resolveHostWorkspace(uiSelection{Tenant: tenant, Environment: environment})
	if err != nil {
		return ""
	}
	return path
}

// stageInvestigationReport writes the report under a host temp dir and returns
// its path for the seed prompt to reference.
func stageInvestigationReport(report string) (string, error) {
	dir := filepath.Join(os.TempDir(), "erun-investigate")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create investigation dir: %w", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("report-%d.md", time.Now().UnixNano()))
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		return "", fmt.Errorf("write investigation report: %w", err)
	}
	return path, nil
}
