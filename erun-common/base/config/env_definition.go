package config

import (
	"github.com/sophium/erun/erun-common/base/model"
)

// PlatformEnvDefinition is the portable EnvConfig subset on the wire. Pointers let
// a pull leave unset fields alone; JSON keys are explicit because several
// EnvConfig fields have no json tag.
type PlatformEnvDefinition struct {
	RuntimeVersion      *string                       `json:"runtimeVersion,omitempty"`
	RuntimeImage        *string                       `json:"runtimeImage,omitempty"`
	RuntimeChart        *string                       `json:"runtimeChart,omitempty"`
	RuntimePod          *model.RuntimePodResources    `json:"runtimePod,omitempty"`
	RuntimeDindPod      *model.RuntimePodResources    `json:"runtimeDindPod,omitempty"`
	NamespaceQuota      *model.NamespaceResourceQuota `json:"namespaceQuota,omitempty"`
	Idle                *EnvironmentIdleConfig        `json:"idle,omitempty"`
	Claude              *EnvironmentClaudeConfig      `json:"claude,omitempty"`
	AITool              *string                       `json:"aiTool,omitempty"`
	AutoUpgrade         *bool                         `json:"autoUpgrade,omitempty"`
	UpgradeChannel      *string                       `json:"upgradeChannel,omitempty"`
	ContainerRegistries *ContainerRegistries          `json:"containerRegistries,omitempty"`
	// DeployTimeout is the portable half of EnvConfig.Deploy.
	DeployTimeout *string `json:"deployTimeout,omitempty"`
}
