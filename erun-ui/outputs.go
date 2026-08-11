package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func quietOutputsContext() eruncommon.Context {
	return eruncommon.Context{
		Logger: eruncommon.NewLoggerWithWriters(0, io.Discard, io.Discard),
		Stdout: io.Discard,
		Stderr: io.Discard,
	}
}

func listAgentOutputsViaRuntime(result eruncommon.OpenResult, params eruncommon.RuntimeOutputsParams) (eruncommon.RuntimeOutputsListResult, error) {
	req := eruncommon.ShellLaunchParamsFromResult(result)
	return eruncommon.ResolveRuntimeOutputs(quietOutputsContext(), req, params, eruncommon.RunRemoteCommand)
}

func downloadAgentOutputViaRuntime(result eruncommon.OpenResult, params eruncommon.RuntimeOutputDownloadParams) (eruncommon.RuntimeOutputResult, error) {
	req := eruncommon.ShellLaunchParamsFromResult(result)
	return eruncommon.DownloadRuntimeOutput(quietOutputsContext(), req, params, eruncommon.RunRemoteCommand)
}

// ListAgentOutputs lists the files an agent produced in the selected env's runtime pod, newest-first.
func (a *App) ListAgentOutputs(selection uiSelection) (eruncommon.RuntimeOutputsListResult, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return eruncommon.RuntimeOutputsListResult{}, fmt.Errorf("tenant and environment are required")
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return eruncommon.RuntimeOutputsListResult{}, err
	}
	return a.deps.listAgentOutputs(result, eruncommon.RuntimeOutputsParams{})
}

// DownloadAgentOutput downloads one entry from the selected env's runtime pod
// and saves it through a native Save dialog. It returns the saved local path,
// or an empty string when the operator cancels the dialog. A folder downloads
// as a <name>.tar.gz archive.
func (a *App) DownloadAgentOutput(selection uiSelection, name string) (string, error) {
	selection = normalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return "", fmt.Errorf("tenant and environment are required")
	}
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("output name is required")
	}
	if a.ctx == nil {
		return "", fmt.Errorf("application context is not ready")
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return "", err
	}
	out, err := a.deps.downloadAgentOutput(result, eruncommon.RuntimeOutputDownloadParams{Name: name})
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
	a.reportHostArtifactSigning(eruncommon.SignHostArtifact(dest))
	return dest, nil
}

// reportHostArtifactSigning tells the operator what ad-hoc signing did to an
// artifact that just landed here. Success is worth one line because the file was
// modified on its way in; a failure is worth one because the alternative is the
// silent SIGKILL macOS answers an unsigned binary with. Both go through the
// notification surface so the message survives the dialog that started it.
func (a *App) reportHostArtifactSigning(signing eruncommon.HostArtifactSigning) {
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
