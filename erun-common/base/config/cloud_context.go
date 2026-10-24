package config

import (
	"fmt"
	"sort"
	"strings"
)

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

const (
	DefaultCloudContextInstanceType = "c8gd.2xlarge"
	DefaultCloudContextDiskType     = "gp3"
	DefaultCloudContextDiskSizeGB   = 100
)

// CloudContextStatus pairs the persisted config with a live, in-memory
// Status this package never writes to disk, so a consumer needing a current
// value must reach a live source rather than a cached one. The StopProtection
// fields are filled only on the dedicated stop-protection paths; the bulk
// refresh skips them to stay one AWS call per (alias,region) group.
type CloudContextStatus struct {
	CloudContextConfig  `json:",inline" yaml:",inline"`
	Status              string `json:"status,omitempty" yaml:"status,omitempty"`
	Message             string `json:"message,omitempty" yaml:"message,omitempty"`
	StopProtection      bool   `json:"stopProtection,omitempty" yaml:"stopprotection,omitempty"`
	StopProtectionKnown bool   `json:"stopProtectionKnown,omitempty" yaml:"stopprotectionknown,omitempty"`
}

func NormalizeCloudContextConfig(config CloudContextConfig) CloudContextConfig {
	config.Name = strings.TrimSpace(config.Name)
	config.Provider = strings.ToLower(strings.TrimSpace(config.Provider))
	config.CloudProviderAlias = strings.TrimSpace(config.CloudProviderAlias)
	config.Region = strings.TrimSpace(config.Region)
	config.InstanceID = strings.TrimSpace(config.InstanceID)
	config.PublicIP = strings.TrimSpace(config.PublicIP)
	config.InstanceType = strings.TrimSpace(config.InstanceType)
	config.DiskType = strings.TrimSpace(config.DiskType)
	config.KubernetesContext = strings.TrimSpace(config.KubernetesContext)
	if config.Name == "" {
		config.Name = config.KubernetesContext
	}
	config.SecurityGroupID = strings.TrimSpace(config.SecurityGroupID)
	config.InstanceProfileName = strings.TrimSpace(config.InstanceProfileName)
	config.InstanceProfileARN = strings.TrimSpace(config.InstanceProfileARN)
	config.InstanceRoleName = strings.TrimSpace(config.InstanceRoleName)
	config.AdminToken = strings.TrimSpace(config.AdminToken)
	config.CreatedAt = strings.TrimSpace(config.CreatedAt)
	config.UpdatedAt = strings.TrimSpace(config.UpdatedAt)
	if config.InstanceType == "" {
		config.InstanceType = DefaultCloudContextInstanceType
	}
	if config.DiskType == "" {
		config.DiskType = DefaultCloudContextDiskType
	}
	if config.DiskSizeGB == 0 {
		config.DiskSizeGB = DefaultCloudContextDiskSizeGB
	}
	if config.KubernetesContext == "" {
		config.KubernetesContext = config.Name
	}
	return config
}

func ListCloudContexts(store CloudReadStore) ([]CloudContextConfig, error) {
	if store == nil {
		return nil, fmt.Errorf("store is required")
	}
	config, _, err := store.LoadERunConfig()
	if err == ErrNotInitialized {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return normalizedCloudContexts(config.CloudContexts), nil
}

func FindCloudContextForKubernetesContext(store CloudReadStore, kubernetesContext string) (CloudContextStatus, bool, error) {
	kubernetesContext = strings.TrimSpace(kubernetesContext)
	if kubernetesContext == "" {
		return CloudContextStatus{}, false, nil
	}
	contexts, err := ListCloudContexts(store)
	if err != nil {
		return CloudContextStatus{}, false, err
	}
	for _, context := range contexts {
		context = NormalizeCloudContextConfig(context)
		if strings.TrimSpace(context.KubernetesContext) == kubernetesContext || strings.TrimSpace(context.Name) == kubernetesContext {
			return CloudContextStatus{CloudContextConfig: context}, true, nil
		}
	}
	return CloudContextStatus{}, false, nil
}

func normalizedCloudContexts(contexts []CloudContextConfig) []CloudContextConfig {
	result := make([]CloudContextConfig, 0, len(contexts))
	for _, context := range contexts {
		context = NormalizeCloudContextConfig(context)
		if context.Name == "" {
			continue
		}
		result = append(result, context)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}
