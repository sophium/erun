package model

// CloudContextConfig is the persisted cloud context; runtime state belongs on CloudContextStatus.
type CloudContextConfig struct {
	Name                string `json:"name" yaml:"name"`
	Provider            string `json:"provider" yaml:"provider"`
	CloudProviderAlias  string `json:"cloudProviderAlias" yaml:"cloudprovideralias"`
	Region              string `json:"region" yaml:"region"`
	InstanceID          string `json:"instanceId,omitempty" yaml:"instanceid,omitempty"`
	PublicIP            string `json:"publicIp,omitempty" yaml:"publicip,omitempty"`
	InstanceType        string `json:"instanceType" yaml:"instancetype"`
	DiskType            string `json:"diskType" yaml:"disktype"`
	DiskSizeGB          int    `json:"diskSizeGb" yaml:"disksizegb"`
	KubernetesContext   string `json:"kubernetesContext" yaml:"kubernetescontext"`
	SecurityGroupID     string `json:"securityGroupId,omitempty" yaml:"securitygroupid,omitempty"`
	InstanceProfileName string `json:"instanceProfileName,omitempty" yaml:"instanceprofilename,omitempty"`
	InstanceProfileARN  string `json:"instanceProfileArn,omitempty" yaml:"instanceprofilearn,omitempty"`
	InstanceRoleName    string `json:"instanceRoleName,omitempty" yaml:"instancerolename,omitempty"`
	AdminToken          string `json:"-" yaml:"admintoken,omitempty"`
	CreatedAt           string `json:"createdAt,omitempty" yaml:"createdat,omitempty"`
	UpdatedAt           string `json:"updatedAt,omitempty" yaml:"updatedat,omitempty"`
}

// CloudContextStatus pairs the config with live Status that is never persisted. StopProtection
// is filled only on stop-protection paths to keep bulk refresh to one AWS call per group.
type CloudContextStatus struct {
	CloudContextConfig  `json:",inline" yaml:",inline"`
	Status              string `json:"status,omitempty" yaml:"status,omitempty"`
	Message             string `json:"message,omitempty" yaml:"message,omitempty"`
	StopProtection      bool   `json:"stopProtection,omitempty" yaml:"stopprotection,omitempty"`
	StopProtectionKnown bool   `json:"stopProtectionKnown,omitempty" yaml:"stopprotectionknown,omitempty"`
}

// CloudContextRefreshFailure is one failed batched status refresh, recorded
// once per (alias, region) group rather than copied onto each context.
type CloudContextRefreshFailure struct {
	Alias   string `json:"alias" yaml:"alias"`
	Region  string `json:"region" yaml:"region"`
	Message string `json:"message" yaml:"message"`
}

// CloudContextListResult is the shared result shape of the CLI's --output json and the context_list MCP tool.
// CloudContexts has no omitempty so an empty list serialises as []; RefreshFailures
// keeps it because the refresh is never attempted when there are no contexts.
type CloudContextListResult struct {
	CloudContexts   []CloudContextStatus         `json:"cloudContexts"`
	RefreshFailures []CloudContextRefreshFailure `json:"refreshFailures,omitempty"`
}

type InitCloudContextParams struct {
	Name               string
	CloudProviderAlias string
	Region             string
	InstanceType       string
	DiskType           string
	DiskSizeGB         int
	SubnetID           string
	SecurityGroupID    string
	KeyName            string
}

type CloudContextParams struct {
	Name  string
	Force bool
}
