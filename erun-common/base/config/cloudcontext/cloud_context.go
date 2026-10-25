package cloudcontext

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
)

const (
	DefaultCloudContextInstanceType = "c8gd.2xlarge"
	DefaultCloudContextDiskType     = "gp3"
	DefaultCloudContextDiskSizeGB   = 100
)

func NormalizeCloudContextConfig(config model.CloudContextConfig) model.CloudContextConfig {
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

func ListCloudContexts(store configpkg.CloudReadStore) ([]model.CloudContextConfig, error) {
	if store == nil {
		return nil, fmt.Errorf("store is required")
	}
	config, _, err := store.LoadERunConfig()
	if errors.Is(err, configpkg.ErrNotInitialized) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return normalizedCloudContexts(config.CloudContexts), nil
}

func FindCloudContextForKubernetesContext(store configpkg.CloudReadStore, kubernetesContext string) (model.CloudContextStatus, bool, error) {
	kubernetesContext = strings.TrimSpace(kubernetesContext)
	if kubernetesContext == "" {
		return model.CloudContextStatus{}, false, nil
	}
	contexts, err := ListCloudContexts(store)
	if err != nil {
		return model.CloudContextStatus{}, false, err
	}
	for _, context := range contexts {
		context = NormalizeCloudContextConfig(context)
		if strings.TrimSpace(context.KubernetesContext) == kubernetesContext || strings.TrimSpace(context.Name) == kubernetesContext {
			return model.CloudContextStatus{CloudContextConfig: context}, true, nil
		}
	}
	return model.CloudContextStatus{}, false, nil
}

func normalizedCloudContexts(contexts []model.CloudContextConfig) []model.CloudContextConfig {
	result := make([]model.CloudContextConfig, 0, len(contexts))
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
