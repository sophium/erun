package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/codesign"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func quietOutputsContext() core.Context {
	return core.Context{
		Logger: core.NewLoggerWithWriters(0, io.Discard, io.Discard),
		Stdout: io.Discard,
		Stderr: io.Discard,
	}
}

func listAgentOutputsViaRuntime(result config.OpenResult, params model.RuntimeOutputsParams) (model.RuntimeOutputsListResult, error) {
	req := cloud.ShellLaunchParamsFromResult(result)
	return jobs.ResolveRuntimeOutputs(quietOutputsContext(), req, params, kube.RunRemoteCommand)
}

func downloadAgentOutputViaRuntime(result config.OpenResult, params model.RuntimeOutputDownloadParams) (model.RuntimeOutputResult, error) {
	req := cloud.ShellLaunchParamsFromResult(result)
	return jobs.DownloadRuntimeOutput(quietOutputsContext(), req, params, kube.RunRemoteCommand)
}

// ListAgentOutputs lists the files an agent produced in the selected env's runtime pod, newest-first.
func (a *App) ListAgentOutputs(selection uimodel.Selection) (model.RuntimeOutputsListResult, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("list agent outputs", selection.Tenant, selection.Environment); err != nil {
		return model.RuntimeOutputsListResult{}, err
	}
	result, err := resolve.ResolveOpen(a.deps.store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return model.RuntimeOutputsListResult{}, err
	}
	return a.deps.listAgentOutputs(result, model.RuntimeOutputsParams{})
}

// DownloadAgentOutput saves one runtime output via a Save dialog; "" means cancelled.
func (a *App) DownloadAgentOutput(selection uimodel.Selection, name string) (string, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("download agent output", selection.Tenant, selection.Environment); err != nil {
		return "", err
	}
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("output name is required")
	}
	if a.ctx == nil {
		return "", fmt.Errorf("application context is not ready")
	}
	result, err := resolve.ResolveOpen(a.deps.store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return "", err
	}
	out, err := a.deps.downloadAgentOutput(result, model.RuntimeOutputDownloadParams{Name: name})
	if err != nil {
		return "", err
	}
	dest, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Save agent output",
		DefaultFilename: out.Name,
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(dest) == "" {
		return "", nil // operator cancelled the dialog
	}
	if err := os.WriteFile(dest, out.Bytes, 0o644); err != nil {
		return "", err
	}
	a.reportHostArtifactSigning(codesign.SignHostArtifact(dest))
	return dest, nil
}

// reportHostArtifactSigning surfaces ad-hoc signing, since macOS SIGKILLs unsigned binaries silently.
func (a *App) reportHostArtifactSigning(signing model.HostArtifactSigning) {
	note := signing.Describe()
	if note == "" {
		return
	}
	kind := "warning"
	if signing.Signed {
		kind = "info"
	}
	a.emitAppNotification(kind, note)
}
