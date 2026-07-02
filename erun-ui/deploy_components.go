package main

import (
	"fmt"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// LoadDeployComponents returns the deployable components for an environment: the
// local component charts discovered under <tenant>-devops/k8s plus the runtime
// item (a local <tenant>-devops/erun-devops chart if present, otherwise the
// published erun-devops chart). Each is flagged with whether it is in the env's
// current resolved default selection (saved deploy.components, else the repo
// k8s.deployments plan, else — when both are empty — the runtime alone). The
// Runtime tab renders this as the "Components to deploy" checklist so the
// operator sees and edits exactly what `erun deploy` will roll out.
//
// Read-only: it resolves the target and discovers charts, but never builds,
// pushes, deploys, or requires a version. It reuses the shared resolver so the
// checklist matches an equivalent `erun deploy` (with no --components).
func (a *App) LoadDeployComponents(selection uiSelection) ([]eruncommon.DeployableComponent, error) {
	selection = normalizeSelection(selection)
	tenant := strings.TrimSpace(selection.Tenant)
	environment := strings.TrimSpace(selection.Environment)
	if tenant == "" || environment == "" {
		return nil, fmt.Errorf("tenant and environment are required")
	}
	return eruncommon.ResolveDeployableComponents(
		a.deps.store,
		a.deps.findProjectRoot,
		nil,
		nil,
		nil,
		eruncommon.DeployTarget{Tenant: tenant, Environment: environment},
	)
}
