package initcmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/cmd/shared/clicmd"
	promptpkg "github.com/sophium/erun/cmd/shared/prompt"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/command/setup"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/spf13/cobra"
)

const (
	initializeCurrentProjectOption       = "Initialize current project"
	enterKubernetesContextManuallyOption = "Enter Kubernetes context manually"
)

func NewInitCmd(runInit func(core.Context, seam.BootstrapInitParams) error) *cobra.Command {
	params := seam.BootstrapInitParams{}
	setDefaultTenant := false
	confirmEnvironment := false
	clusterRegistry := false
	erunRegistry := false
	var envType string
	var components []string

	cmd := &cobra.Command{
		Use:          "init [TENANT] [ENVIRONMENT]",
		Short:        "Initialize configuration for the current project",
		Args:         cobra.MaximumNArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			runParams, err := initRunParams(cmd, args, params, setDefaultTenant, confirmEnvironment, clusterRegistry, erunRegistry, envType, components)
			if err != nil {
				return err
			}
			return runInit(clicmd.CommandContext(cmd), runParams)
		},
	}

	cmd.Flags().StringVar(&params.Tenant, "tenant", "", "Tenant name to initialize")
	cmd.Flags().StringVar(&params.ProjectRoot, "project-root", "", "Project root to bind to the tenant")
	cmd.Flags().StringVar(&params.Environment, "environment", "", "Environment name")
	cmd.Flags().StringVar(&params.RuntimeVersion, "version", "", "Runtime image version to record; omitted, a new env takes this erun's version and an existing env keeps the one it runs. A brand-new remote-worktree env (--type remote-agent or runtime) deploys at this version immediately; on an existing env this only re-pins the version in config — run a separate erun deploy to actually roll it out")
	cmd.Flags().StringVar(&params.RuntimeImage, "runtime-image", "", "Runtime image repository to initialize and deploy")
	cmd.Flags().StringVar(&params.RuntimeRegistry, "runtime-registry", "", "Registry the environment resolves erun's own artifacts from — the runtime chart and the platform images the pod pulls (e.g. ghcr.io/sophium). Set it when the environment's deploy registry holds only this project's images, so erun's chart is not there to pull; persisted as the env's runtimeregistry")
	cmd.Flags().StringSliceVar(&params.ImagePullSecrets, "image-pull-secret", nil, "Kubernetes dockerconfigjson secret the runtime pod pulls its image with; repeat or comma-separate for several. Required when the runtime image is in a private registry")
	cmd.Flags().StringVar(&params.RuntimePod.CPU, "runtime-cpu", "", "Runtime pod CPU limit")
	cmd.Flags().StringVar(&params.RuntimePod.Memory, "runtime-memory", "", "Runtime pod memory limit")
	cmd.Flags().StringVar(&params.RuntimeDindPod.CPU, "dind-cpu", "", "erun-dind sidecar CPU limit — the container that actually runs erun build/erun release")
	cmd.Flags().StringVar(&params.RuntimeDindPod.Memory, "dind-memory", "", "erun-dind sidecar memory limit; raise this when a multi-arch erun release/erun build --release OOMs inside the sidecar")
	cmd.Flags().StringVar(&params.KubernetesContext, "kubernetes-context", "", "Kubernetes context to associate with the environment")
	cmd.Flags().StringVar(&params.ContainerRegistry, "container-registry", "", "Container registry to associate with the environment")
	cmd.Flags().BoolVar(&clusterRegistry, "cluster-registry", false, "Use the in-cluster erun-registry (addresses resolved from the env's kube-context) instead of --container-registry; mutually exclusive with it")
	cmd.Flags().BoolVar(&erunRegistry, "erun-registry", false, "Use erun's hosted container registry (registry.erunpaas.com/<tenant>), authenticated by this tenant's own API token, instead of --container-registry; mutually exclusive with it and with --cluster-registry")
	cmd.Flags().StringVar(&params.CodeCommitSSHKeyID, "codecommit-ssh-key-id", "", "CodeCommit SSH public key ID to use for remote repository access")
	cmd.Flags().BoolVar(&params.Bootstrap, "bootstrap", false, "Deprecated: ignored; remote runtimes deploy the published erun-devops chart")
	_ = cmd.Flags().MarkDeprecated("bootstrap", "remote runtimes deploy the published erun-devops chart; the flag is ignored")
	cmd.Flags().StringVar(&envType, "type", "", "Environment type: local-agent, remote-agent, runtime, or host (takes precedence over --remote). host names a directory on this machine with no pod and no cluster at all — for desktop-app builds and tasks needing host-wide credentials. On an existing env this changes the type; omit it and the env keeps the type it has")
	cmd.Flags().BoolVar(&params.Remote, "remote", false, "Deprecated alias for --type=remote-agent")
	cmd.Flags().BoolVar(&params.NoGit, "no-git", false, "Skip remote Git checkout setup when used with --remote or --type=remote-agent")
	cmd.Flags().BoolVar(&setDefaultTenant, "set-default-tenant", false, "Set the initialized tenant as the default tenant")
	cmd.Flags().BoolVar(&confirmEnvironment, "confirm-environment", false, "Confirm environment initialization without prompting")
	cmd.Flags().BoolVarP(&params.AutoApprove, "yes", "y", false, "Automatically approve initialization prompts")
	cmd.Flags().BoolVar(&params.DisableBuildScript, "disable-build-script", false, "Ignore any project build.sh for this env; erun build resolves docker/release contexts directly")
	cmd.Flags().BoolVar(&params.PlatformAccount, "platform-account", false, "Make this env a cluster platform account: deploy binds its runtime ServiceAccount to cluster-admin so in-pod platform terraform (cluster edge) and component installs can manage cluster-scoped resources")
	cmd.Flags().StringVar(&params.MCPAuthPublicKeyPath, "mcp-auth-public-key", "", "Require the env's MCP edge to authenticate bearer tokens signed by this PEM public key; empty leaves the edge loopback-only. Folds MCP auth into init's single runtime deploy.")
	cmd.Flags().StringSliceVar(&components, "components", nil, "Save this as the env's default deploy component selection — what a later erun deploy rolls out with no --components flag of its own. Pass an empty string to clear a saved selection and return the env to its repo k8s.deployments plan")
	clicmd.AddDryRunFlag(cmd)
	return cmd
}

func initRunParams(cmd *cobra.Command, args []string, params seam.BootstrapInitParams, setDefaultTenant, confirmEnvironment, clusterRegistry, erunRegistry bool, envType string, components []string) (seam.BootstrapInitParams, error) {
	runParams := params
	applyPositionalInitArgs(&runParams, args)
	if err := applyClusterRegistryFlag(&runParams, clusterRegistry); err != nil {
		return seam.BootstrapInitParams{}, err
	}
	if err := applyErunRegistryFlag(&runParams, erunRegistry, clusterRegistry); err != nil {
		return seam.BootstrapInitParams{}, err
	}
	if err := applyInitTypeFlag(cmd, &runParams, envType); err != nil {
		return seam.BootstrapInitParams{}, err
	}
	if err := validateInitRunParams(runParams); err != nil {
		return seam.BootstrapInitParams{}, err
	}
	if cmd.Flags().Changed("set-default-tenant") {
		runParams.ConfirmTenant = &setDefaultTenant
	}
	if cmd.Flags().Changed("confirm-environment") {
		runParams.ConfirmEnvironment = &confirmEnvironment
	}
	if cmd.Flags().Changed("components") {
		runParams.Components = &components
	}
	return runParams, nil
}

func applyPositionalInitArgs(runParams *seam.BootstrapInitParams, args []string) {
	if runParams.Tenant == "" && len(args) > 0 {
		runParams.Tenant = args[0]
	}
	if runParams.Environment == "" && len(args) > 1 {
		runParams.Environment = args[1]
	}
}

func applyClusterRegistryFlag(runParams *seam.BootstrapInitParams, clusterRegistry bool) error {
	if !clusterRegistry {
		return nil
	}
	if strings.TrimSpace(runParams.ContainerRegistry) != "" {
		return fmt.Errorf("--cluster-registry conflicts with --container-registry; pick one")
	}
	// The in-cluster erun-registry is plain HTTP.
	runParams.ClusterRegistry = &config.ClusterRegistry{Insecure: true}
	return nil
}

func applyErunRegistryFlag(runParams *seam.BootstrapInitParams, erunRegistry, clusterRegistry bool) error {
	if !erunRegistry {
		return nil
	}
	if clusterRegistry {
		return fmt.Errorf("--erun-registry conflicts with --cluster-registry; pick one")
	}
	if strings.TrimSpace(runParams.ContainerRegistry) != "" {
		return fmt.Errorf("--erun-registry conflicts with --container-registry; pick one")
	}
	runParams.ErunRegistry = true
	return nil
}

func applyInitTypeFlag(cmd *cobra.Command, runParams *seam.BootstrapInitParams, envType string) error {
	envType = strings.TrimSpace(envType)
	if envType == "" {
		return nil
	}
	parsed := config.EnvironmentType(envType)
	if !parsed.IsValid() {
		return fmt.Errorf("invalid --type %q: must be local-agent, remote-agent, runtime, or host", envType)
	}
	// Derived from RemoteWorktree so host reports false.
	expectedRemote := (seam.BootstrapInitParams{Type: parsed}).RemoteWorktree()
	remoteFlag := cmd.Flags().Lookup("remote")
	remoteChanged := remoteFlag != nil && remoteFlag.Changed
	if remoteChanged && runParams.Remote != expectedRemote {
		return fmt.Errorf("--type=%s conflicts with --remote=%t", envType, runParams.Remote)
	}
	runParams.Type = parsed
	runParams.Remote = expectedRemote
	return nil
}

func validateInitRunParams(params seam.BootstrapInitParams) error {
	if params.RemoteWorktree() && params.Tenant == "" {
		return fmt.Errorf("tenant is required for non-local-agent envs")
	}
	if params.RemoteWorktree() && strings.TrimSpace(params.Environment) == "" {
		return fmt.Errorf("environment is required for non-local-agent envs")
	}
	return nil
}

func ContainerRegistryPrompt(run promptpkg.PromptRunner, label string) (string, error) {
	prompt := promptui.Prompt{
		Label:   label,
		Default: config.DefaultContainerRegistry,
	}

	result, err := run(prompt)
	if err != nil {
		if errors.Is(err, promptui.ErrInterrupt) {
			return "", fmt.Errorf("container registry configuration interrupted")
		}
		if errors.Is(err, promptui.ErrAbort) {
			return "", setup.ErrContainerRegistryCancelled
		}
		return "", err
	}

	result = strings.TrimSpace(result)
	if result == "" {
		return config.DefaultContainerRegistry, nil
	}
	return result, nil
}

func RemoteRepositoryURLPrompt(run promptpkg.PromptRunner, label string) (string, error) {
	prompt := promptui.Prompt{
		Label: label,
		Validate: func(input string) error {
			if strings.TrimSpace(input) == "" {
				return fmt.Errorf("repository remote URL is required")
			}
			return nil
		},
	}

	result, err := run(prompt)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result), nil
}

func CodeCommitSSHKeyIDPrompt(run promptpkg.PromptRunner, label string) (string, error) {
	prompt := promptui.Prompt{
		Label: label,
		Validate: func(input string) error {
			if strings.TrimSpace(input) == "" {
				return fmt.Errorf("CodeCommit SSH public key ID is required")
			}
			return nil
		},
	}

	result, err := run(prompt)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result), nil
}

func KubernetesContextPrompt(run promptpkg.PromptRunner, selectRun promptpkg.SelectRunner, list promptpkg.KubernetesContextsLister, label string) (string, error) {
	contexts := listedKubernetesContexts(list)
	if len(contexts) > 0 && selectRun != nil {
		selected, manual, err := selectKubernetesContextPrompt(selectRun, label, contexts)
		if err != nil {
			return "", err
		}
		if !manual {
			return selected, nil
		}
	}

	return manualKubernetesContextPrompt(run, label)
}

func listedKubernetesContexts(list promptpkg.KubernetesContextsLister) []string {
	if list == nil {
		return nil
	}
	contexts, err := list()
	if err != nil {
		return nil
	}
	return normalizeKubernetesContexts(contexts)
}

func manualKubernetesContextPrompt(run promptpkg.PromptRunner, label string) (string, error) {
	prompt := promptui.Prompt{
		Label: label,
		Validate: func(input string) error {
			if strings.TrimSpace(input) == "" {
				return fmt.Errorf("kubernetes context is required")
			}
			return nil
		},
	}

	result, err := run(prompt)
	if err != nil {
		if errors.Is(err, promptui.ErrInterrupt) {
			return "", fmt.Errorf("kubernetes context association interrupted")
		}
		if errors.Is(err, promptui.ErrAbort) {
			return "", setup.ErrKubernetesContextCancelled
		}
		return "", err
	}

	result = strings.TrimSpace(result)
	if result == "" {
		return "", setup.ErrKubernetesContextCancelled
	}
	return result, nil
}

func selectKubernetesContextPrompt(run promptpkg.SelectRunner, label string, contexts []string) (string, bool, error) {
	items := append(append([]string{}, contexts...), enterKubernetesContextManuallyOption)

	prompt := promptui.Select{
		Label: label,
		Items: items,
	}

	_, result, err := run(prompt)
	if err != nil {
		if errors.Is(err, promptui.ErrInterrupt) {
			return "", false, fmt.Errorf("kubernetes context selection interrupted")
		}
		if errors.Is(err, promptui.ErrAbort) {
			return "", false, setup.ErrKubernetesContextCancelled
		}
		return "", false, err
	}

	if result == enterKubernetesContextManuallyOption {
		return "", true, nil
	}
	return strings.TrimSpace(result), false, nil
}

func normalizeKubernetesContexts(contexts []string) []string {
	seen := make(map[string]struct{}, len(contexts))
	result := make([]string, 0, len(contexts))
	for _, context := range contexts {
		context = strings.TrimSpace(context)
		if context == "" {
			continue
		}
		if _, ok := seen[context]; ok {
			continue
		}
		seen[context] = struct{}{}
		result = append(result, context)
	}
	return result
}

func ListKubernetesContexts() ([]string, error) {
	output, err := core.Command("kubectl", "config", "get-contexts", "-o=name").Output()
	if err != nil {
		return nil, err
	}
	contexts := strings.Split(string(output), "\n")

	currentOutput, err := core.Command("kubectl", "config", "current-context").Output()
	if err == nil {
		contexts = preferCurrentKubernetesContext(contexts, string(currentOutput))
	}

	return contexts, nil
}

func EnsureKubernetesNamespace(contextName, namespace string) error {
	return kube.EnsureKubernetesNamespace(contextName, namespace)
}

func preferCurrentKubernetesContext(contexts []string, current string) []string {
	current = strings.TrimSpace(current)
	if current == "" {
		return contexts
	}

	result := make([]string, 0, len(contexts))
	result = append(result, current)
	for _, context := range contexts {
		if strings.TrimSpace(context) == current {
			continue
		}
		result = append(result, context)
	}
	return result
}

// tenantPromptHasAnswer peeks so piped answers still work; only an exhausted stdin is unanswerable.
var tenantPromptHasAnswer = func() bool {
	_, err := promptpkg.PlainPromptInput().Peek(1)
	return err == nil
}

// TenantSelectionUnavailable reports whether stdin cannot answer a tenant prompt.
func TenantSelectionUnavailable() bool {
	return !promptpkg.StdinIsTerminal() && !tenantPromptHasAnswer()
}

func SelectTenantPrompt(run promptpkg.SelectRunner, tenants []model.TenantConfig) (model.TenantSelectionResult, error) {
	items := make([]string, 0, len(tenants)+1)
	for _, tenant := range tenants {
		items = append(items, tenant.Name)
	}
	items = append(items, initializeCurrentProjectOption)

	prompt := promptui.Select{
		Label: "Select tenant",
		Items: items,
	}

	_, result, err := run(prompt)
	if err != nil {
		if errors.Is(err, promptui.ErrInterrupt) {
			return model.TenantSelectionResult{}, fmt.Errorf("tenant selection interrupted")
		}
		if errors.Is(err, promptui.ErrAbort) {
			return model.TenantSelectionResult{}, nil
		}
		return model.TenantSelectionResult{}, err
	}

	if result == initializeCurrentProjectOption {
		return model.TenantSelectionResult{Initialize: true}, nil
	}

	return model.TenantSelectionResult{Tenant: result}, nil
}
