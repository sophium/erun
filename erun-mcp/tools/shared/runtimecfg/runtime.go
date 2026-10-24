package runtimecfg

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/command/setup"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
	"github.com/sophium/erun/erun-common/primitive/shared/projectseam"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

type RuntimeStore interface {
	setup.BootstrapStore
	lifecycle.ListStore
	projectseam.DockerStore
	deploy.DeployStore
}

type RuntimeConfig struct {
	Context                        toolio.RuntimeContext
	Store                          RuntimeStore
	BuildScriptRunner              build.BuildScriptRunnerFunc
	BuildDockerImage               build.DockerImageBuilderFunc
	PushDockerImage                dockerspec.DockerImagePusherFunc
	DeployHelmChart                deploy.HelmChartDeployerFunc
	EnsureKubernetesNamespace      deploy.NamespaceEnsurerFunc
	DeleteKubernetesNamespace      lifecycle.NamespaceDeleterFunc
	WaitForRemoteRuntime           setup.RemoteRuntimeWaitFunc
	RunRemoteCommand               kube.RemoteCommandRunnerFunc
	ResolveRuntimeRegistryVersions registry.RuntimeRegistryVersionResolverFunc
}

// ResolveLocalTarget resolves the tool's tenant/environment and refuses any other
// environment: this server can only act on its own pod.
func ResolveLocalTarget(runtime RuntimeConfig, tenant, environment string) (string, string, error) {
	resolvedTenant, resolvedEnvironment, err := matchOrDefaultLocalTarget(
		strings.TrimSpace(runtime.Context.Tenant), strings.TrimSpace(runtime.Context.Environment),
		tenant, environment,
	)
	if err != nil {
		return "", "", err
	}
	if resolvedTenant == "" || resolvedEnvironment == "" {
		return "", "", ErrMissingLocalTarget(resolvedTenant == "", resolvedEnvironment == "")
	}
	return resolvedTenant, resolvedEnvironment, nil
}

// matchOrDefaultLocalTarget refuses a different requested environment, naming both
// and the remedy; an empty field defaults to the server's own.
func matchOrDefaultLocalTarget(serverTenant, serverEnvironment, requestedTenant, requestedEnvironment string) (tenant, environment string, err error) {
	requestedTenant = strings.TrimSpace(requestedTenant)
	requestedEnvironment = strings.TrimSpace(requestedEnvironment)
	tenantMismatch := requestedTenant != "" && serverTenant != "" && requestedTenant != serverTenant
	environmentMismatch := requestedEnvironment != "" && serverEnvironment != "" && requestedEnvironment != serverEnvironment
	if tenantMismatch || environmentMismatch {
		return "", "", fmt.Errorf(
			"this MCP server serves %s/%s and runs every tool there, so it cannot act on %s/%s: call that environment's own MCP edge instead",
			serverTenant, serverEnvironment,
			toolio.FirstNonEmpty(requestedTenant, serverTenant), toolio.FirstNonEmpty(requestedEnvironment, serverEnvironment),
		)
	}
	return toolio.FirstNonEmpty(requestedTenant, serverTenant), toolio.FirstNonEmpty(requestedEnvironment, serverEnvironment), nil
}

// ErrMissingLocalTarget names what is missing and how to recover.
func ErrMissingLocalTarget(missingTenant, missingEnvironment bool) error {
	var missing []string
	if missingTenant {
		missing = append(missing, "tenant")
	}
	if missingEnvironment {
		missing = append(missing, "environment")
	}
	what := strings.Join(missing, "/")
	return fmt.Errorf(
		"%s not resolved: this MCP server was not started bound to a %s, and the call did not supply %s either -- pass %s explicitly in the call, or run this edge for an environment that has it configured",
		what, what, what, strings.Join(missing, " and "),
	)
}
