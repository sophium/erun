package config

// CloudContextConfig is the on-disk shape for a managed cloud context.
// It must contain only configuration the user actually authors —
// runtime/operational fields (current power state, AWS-observed status)
// belong on CloudContextStatus and are never persisted.
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
