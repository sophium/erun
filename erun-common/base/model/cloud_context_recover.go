package model

// RecoverCloudContextParams carries inputs the orphan record cannot supply.
type RecoverCloudContextParams struct {
	KubernetesContext  string
	CloudProviderAlias string
	Region             string
	AdminToken         string
}

// RecoverCloudContextResult records what the recovery saved.
type RecoverCloudContextResult struct {
	Saved     CloudContextConfig
	Source    string // "aws-describe-instances"
	TokenFrom string // "kubeconfig" | "input" | "none"
}
