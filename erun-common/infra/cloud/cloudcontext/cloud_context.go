package cloudcontext

import (
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/cloudcontext"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

const (
	AlternateCloudContextInstanceType = "t4g.xlarge"
	AlternateCloudContextDiskSizeGB   = 200
	DefaultCloudContextRegion         = "eu-west-2"
	AlternateCloudContextRegion       = "eu-west-1"

	CloudContextStatusPending = "pending"
	CloudContextStatusRunning = "running"
	CloudContextStatusStopped = "stopped"
	CloudContextStatusUnknown = "unknown"
)

// CloudContextEnvLookup lets StartCloudContext enforce working hours; stores without it skip the gate.
type CloudContextEnvLookup interface {
	ListTenantConfigs() ([]model.TenantConfig, error)
	ListEnvConfigs(string) ([]configpkg.EnvConfig, error)
}

type CloudContextDependencies struct {
	RunAWS     func(core.Context, model.CloudProviderConfig, string, []string) (string, error)
	RunKubectl func(core.Context, []string) error
	Now        func() time.Time
	Sleep      func(time.Duration)
	NewToken   func() string
}

func CloudContextInstanceTypes() []string {
	return []string{cloudcontext.DefaultCloudContextInstanceType, AlternateCloudContextInstanceType}
}

func CloudContextDiskSizesGB() []int {
	return []int{cloudcontext.DefaultCloudContextDiskSizeGB, AlternateCloudContextDiskSizeGB}
}

func CloudContextRegions() []string {
	return []string{DefaultCloudContextRegion, AlternateCloudContextRegion}
}

func ListCloudContextStatuses(store configpkg.CloudReadStore) ([]model.CloudContextStatus, error) {
	contexts, err := cloudcontext.ListCloudContexts(store)
	if err != nil {
		return nil, err
	}
	statuses := make([]model.CloudContextStatus, 0, len(contexts))
	for _, context := range contexts {
		statuses = append(statuses, model.CloudContextStatus{CloudContextConfig: context})
	}
	return statuses, nil
}
