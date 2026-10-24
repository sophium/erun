package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/codesign"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/open"
	"github.com/sophium/erun/erun-common/primitive/shared"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func quietOutputsContext() core.Context {
	return core.Context{
		Logger: core.NewLoggerWithWriters(0, io.Discard, io.Discard),
		Stdout: io.Discard,
		Stderr: io.Discard,
	}
}

func listAgentOutputsViaRuntime(result config.OpenResult, params jobs.RuntimeOutputsParams) (jobs.RuntimeOutputsListResult, error) {
	req := cloud.ShellLaunchParamsFromResult(result)
	return jobs.ResolveRuntimeOutputs(quietOutputsContext(), req, params, kube.RunRemoteCommand)
}

func downloadAgentOutputViaRuntime(result config.OpenResult, params jobs.RuntimeOutputDownloadParams) (jobs.RuntimeOutputResult, error) {
	req := cloud.ShellLaunchParamsFromResult(result)
	return jobs.DownloadRuntimeOutput(quietOutputsContext(), req, params, kube.RunRemoteCommand)
}

// ListAgentOutputs lists the files an agent produced in the selected env's runtime pod, newest-first.
func (a *App) ListAgentOutputs(selection uiSelection) (jobs.RuntimeOutputsListResult, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("list agent outputs", selection.Tenant, selection.Environment); err != nil {
		return jobs.RuntimeOutputsListResult{}, err
	}
	result, err := open.ResolveOpen(a.deps.store, shared.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return jobs.RuntimeOutputsListResult{}, err
	}
	return a.deps.listAgentOutputs(result, jobs.RuntimeOutputsParams{})
}

// DownloadAgentOutput downloads one entry from the selected env's runtime pod
// and saves it through a native Save dialog. It returns the saved local path,
// or an empty string when the operator cancels the dialog. A folder downloads
// as a <name>.tar.gz archive.
func (a *App) DownloadAgentOutput(selection uiSelection, name string) (string, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("download agent output", selection.Tenant, selection.Environment); err != nil {
		return "", err
	}
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("output name is required")
	}
	if a.ctx == nil {
		return "", fmt.Errorf("application context is not ready")
	}
	result, err := open.ResolveOpen(a.deps.store, shared.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return "", err
	}
	out, err := a.deps.downloadAgentOutput(result, jobs.RuntimeOutputDownloadParams{Name: name})
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

// reportHostArtifactSigning tells the operator what ad-hoc signing did to an
// artifact that just landed here. Success is worth one line because the file was
// modified on its way in; a failure is worth one because the alternative is the
// silent SIGKILL macOS answers an unsigned binary with. Both go through the
// notification surface so the message survives the dialog that started it.
func (a *App) reportHostArtifactSigning(signing codesign.HostArtifactSigning) {
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
