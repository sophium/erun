package erunmcp

import (
	"context"
	"errors"
	"io"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
	"github.com/sophium/erun/erun-common/base/seam/imagebuild"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
)

var errSeamNotWired = errors.New("seam not wired in this test")

// withStubRuntime replaces every seam with a refusing stub and fills an unset
// store. The production defaults are applied by cmd/emcp, so they are not
// linked into this package.
func withStubRuntime(cfg runtimecfg.RuntimeConfig) runtimecfg.RuntimeConfig {
	if cfg.Store == nil {
		cfg.Store = config.ConfigStore{}
	}
	cfg.BuildScriptRunner = func(string, string, []string, io.Reader, io.Writer, io.Writer) error { return errSeamNotWired }
	cfg.BuildDockerImage = func(imagebuild.DockerBuildSpec, io.Writer, io.Writer) error { return errSeamNotWired }
	cfg.PushDockerImage = func(string, int, io.Writer, io.Writer) error { return errSeamNotWired }
	cfg.DeployHelmChart = func(helmdeploy.HelmDeployParams) error { return errSeamNotWired }
	cfg.EnsureKubernetesNamespace = func(string, string) error { return errSeamNotWired }
	cfg.DeleteKubernetesNamespace = func(string, string) error { return errSeamNotWired }
	cfg.WaitForRemoteRuntime = func(config.ShellLaunchParams) error { return errSeamNotWired }
	cfg.RunRemoteCommand = func(config.ShellLaunchParams, string) (model.RemoteCommandResult, error) {
		return model.RemoteCommandResult{}, errSeamNotWired
	}
	cfg.ResolveRuntimeRegistryVersions = func(context.Context) (model.RuntimeRegistryVersions, error) {
		return model.RuntimeRegistryVersions{}, errSeamNotWired
	}
	return cfg
}
