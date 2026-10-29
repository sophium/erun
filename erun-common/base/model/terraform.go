package model

// TerraformOperation is the verb RunTerraform performs against a per-env root.
type TerraformOperation string

const (
	TerraformInit    TerraformOperation = "init"
	TerraformApply   TerraformOperation = "apply"
	TerraformPlan    TerraformOperation = "plan"
	TerraformDestroy TerraformOperation = "destroy"
)

// TerraformParams are RunTerraform's inputs; empty Tenant/Environment use the
// configured default.
type TerraformParams struct {
	Tenant      string
	Environment string
	Operation   TerraformOperation
	ProjectRoot string
	ExtraArgs   []string
}

// TerraformResult is the resolved Terraform plan a run would execute.
type TerraformResult struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Operation   string `json:"operation"`
	Namespace   string `json:"namespace"`
	Directory   string `json:"directory"`
	// RootSource is "configured", "repo-root", or "devops".
	RootSource              string `json:"rootSource,omitempty"`
	ConfiguredTerraformBase string `json:"configuredTerraformBase,omitempty"`
	VarFile                 string `json:"varFile,omitempty"`
	// WorkDir, StateFile and DataDir live on the home PVC, off the read-only
	// playbook tree, so state survives a pod restart.
	WorkDir   string `json:"workDir,omitempty"`
	StateFile string `json:"stateFile,omitempty"`
	DataDir   string `json:"dataDir,omitempty"`
	EnvType   string `json:"envType,omitempty"`
	// StateLocus says whether WorkDir is this host's home or the target pod's PVC.
	StateLocus string `json:"stateLocus,omitempty"`
	// KubernetesContext is the context the RFC2136 Secret is read from.
	KubernetesContext string `json:"kubernetesContext,omitempty"`
	// LockReadonly means a baked lock file pins providers, so init never rewrites
	// the read-only tree.
	LockReadonly bool `json:"lockReadonly,omitempty"`
	// LegacyStateInTree flags an ignored pre-relocation terraform.tfstate.
	LegacyStateInTree bool `json:"legacyStateInTree,omitempty"`
	// RFC2136SecretName and RFC2136SecretNamespace name the TSIG Secret's source;
	// its value is never logged or serialized.
	RFC2136SecretName      string     `json:"rfc2136SecretName,omitempty"`
	RFC2136SecretNamespace string     `json:"rfc2136SecretNamespace,omitempty"`
	Commands               [][]string `json:"commands"`
	RequiresConfirmation   bool       `json:"requiresConfirmation"`
	// Dispatched means terraform ran inside the target pod; the path fields are
	// then unset.
	Dispatched bool `json:"dispatched,omitempty"`
	// Stdout and Stderr carry the dispatched run's output.
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
}
