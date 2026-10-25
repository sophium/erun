package envops

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
)

func quietOutputsContext() core.Context {
	return core.Context{
		Logger: core.NewLoggerWithWriters(0, io.Discard, io.Discard),
		Stdout: io.Discard,
		Stderr: io.Discard,
	}
}

// ListAgentOutputsViaRuntime is the production ListAgentOutputs seam.
func ListAgentOutputsViaRuntime(result config.OpenResult, params model.RuntimeOutputsParams) (model.RuntimeOutputsListResult, error) {
	req := cloud.ShellLaunchParamsFromResult(result)
	return jobs.ResolveRuntimeOutputs(quietOutputsContext(), req, params, kube.RunRemoteCommand)
}

// DownloadAgentOutputViaRuntime is the production DownloadAgentOutput seam.
func DownloadAgentOutputViaRuntime(result config.OpenResult, params model.RuntimeOutputDownloadParams) (model.RuntimeOutputResult, error) {
	req := cloud.ShellLaunchParamsFromResult(result)
	return jobs.DownloadRuntimeOutput(quietOutputsContext(), req, params, kube.RunRemoteCommand)
}

// ListAgentOutputs lists the files an agent produced in the selected env's runtime pod, newest-first.
func (s *Service) ListAgentOutputs(selection uimodel.Selection) (model.RuntimeOutputsListResult, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("list agent outputs", selection.Tenant, selection.Environment); err != nil {
		return model.RuntimeOutputsListResult{}, err
	}
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return model.RuntimeOutputsListResult{}, err
	}
	return s.d.ListAgentOutputs(result, model.RuntimeOutputsParams{})
}

// DownloadAgentOutput saves one runtime output where saveAs says; saveAs runs the
// native Save dialog, so it stays with the caller. "" means cancelled.
func (s *Service) DownloadAgentOutput(selection uimodel.Selection, name string, saveAs func(defaultName string) (string, error)) (string, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("download agent output", selection.Tenant, selection.Environment); err != nil {
		return "", err
	}
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("output name is required")
	}
	if !s.d.Started() {
		return "", fmt.Errorf("application context is not ready")
	}
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return "", err
	}
	out, err := s.d.DownloadAgentOutput(result, model.RuntimeOutputDownloadParams{Name: name})
	if err != nil {
		return "", err
	}
	dest, err := saveAs(out.Name)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(dest) == "" {
		return "", nil // operator cancelled the dialog
	}
	if err := os.WriteFile(dest, out.Bytes, 0o644); err != nil {
		return "", err
	}
	s.reportHostArtifactSigning(codesign.SignHostArtifact(dest))
	return dest, nil
}

// SigningNotice describes ad-hoc signing, since macOS SIGKILLs unsigned binaries
// silently; an empty message means there is nothing to say.
func SigningNotice(signing model.HostArtifactSigning) (kind, message string) {
	note := signing.Describe()
	if note == "" {
		return "", ""
	}
	if signing.Signed {
		return "info", note
	}
	return "warning", note
}

func (s *Service) reportHostArtifactSigning(signing model.HostArtifactSigning) {
	if kind, note := SigningNotice(signing); note != "" {
		s.notify(kind, note)
	}
}
