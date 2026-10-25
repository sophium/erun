package main

import (
	"fmt"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

func (a *App) ensureLinkedCloudContextRunning(config configpkg.EnvConfig) (model.CloudContextStatus, bool, error) {
	status, ok, err := a.settings.LinkedCloudContext(config)
	if err != nil || !ok {
		return status, ok, err
	}
	if strings.TrimSpace(status.Status) == cloud.CloudContextStatusRunning {
		a.emitAppStatus(fmt.Sprintf("Cloud context %s is running. Opening environment...", cloudContextDisplayName(status)))
		return status, true, nil
	}
	a.emitAppStatus(fmt.Sprintf("Starting cloud context %s and waiting for Kubernetes access...", cloudContextDisplayName(status)))
	status, err = cloud.StartCloudContext(core.Context{}, a.deps.store, model.CloudContextParams{Name: status.Name}, a.deps.cloudContextDeps)
	if err != nil {
		return model.CloudContextStatus{}, true, err
	}
	a.clearIdleStopsForCloudContext(status.Name)
	a.cloudContexts.SetStatus(status.Name, status.Status)
	a.emitAppStatus(fmt.Sprintf("Cloud context %s is running. Opening environment...", cloudContextDisplayName(status)))
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

func cloudContextDisplayName(status model.CloudContextStatus) string {
	if name := strings.TrimSpace(status.KubernetesContext); name != "" {
		return name
	}
	return strings.TrimSpace(status.Name)
}
