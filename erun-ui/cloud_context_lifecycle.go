package main

import (
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud/cloudcontext"
	"github.com/sophium/erun/erun-common/infra/cloud/contextpower"
	"github.com/sophium/erun/erun-ui/internal/terminal"
)

func (a *App) ensureLinkedCloudContextRunning(config configpkg.EnvConfig) (model.CloudContextStatus, bool, error) {
	status, ok, err := a.settings.LinkedCloudContext(config)
	if err != nil || !ok {
		return status, ok, err
	}
	if strings.TrimSpace(status.Status) == cloudcontext.CloudContextStatusRunning {
		a.emitAppStatus(fmt.Sprintf("Cloud context %s is running. Opening environment...", terminal.CloudContextLabel(status)))
		return status, true, nil
	}
	a.emitAppStatus(fmt.Sprintf("Starting cloud context %s and waiting for Kubernetes access...", terminal.CloudContextLabel(status)))
	status, err = contextpower.StartCloudContext(core.Context{}, a.deps.store, model.CloudContextParams{Name: status.Name}, a.deps.cloudContextDeps)
	if err != nil {
		return model.CloudContextStatus{}, true, err
	}
	a.terminal.ClearIdleStopsForCloudContext(status.Name)
	a.cloudContexts.SetStatus(status.Name, status.Status)
	a.emitAppStatus(fmt.Sprintf("Cloud context %s is running. Opening environment...", terminal.CloudContextLabel(status)))
	return status, true, nil
}

func (a *App) stopCloudContext(name string) (model.CloudContextStatus, error) {
	ctx := a.context()
	status, err := a.deps.stopCloudContext(ctx, name)
	if err != nil {
		return status, err
	}
	a.cloudContexts.SetStatus(status.Name, status.Status)
	return status, nil
}
