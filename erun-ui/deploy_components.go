package main

import (
	"fmt"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// LoadDeployComponents powers the Runtime tab's "Components to deploy" checklist
// so the operator sees and edits exactly what an equivalent `erun deploy` would
// roll out. Read-only: it never builds, pushes, deploys, or requires a version.
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
