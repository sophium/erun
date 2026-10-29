package cloudcontext

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
)

func TestIsInClusterCloudContextDistinguishesPowerManagedContexts(t *testing.T) {
	cases := []struct {
		name   string
		config model.CloudContextConfig
		want   bool
	}{
		{"pod-injected context", model.CloudContextConfig{Name: providerconfig.InClusterKubernetesContext, KubernetesContext: providerconfig.InClusterKubernetesContext}, true},
		{"kube context only", model.CloudContextConfig{Name: "prod-platform", KubernetesContext: providerconfig.InClusterKubernetesContext}, true},
		{"power-managed without an instance", model.CloudContextConfig{Name: "petios-ctx", KubernetesContext: "petios-ctx"}, false},
		{"power-managed with an instance", model.CloudContextConfig{Name: "petios-ctx", KubernetesContext: "petios-ctx", InstanceID: "i-0abc123"}, false},
		{"blank", model.CloudContextConfig{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsInClusterCloudContext(tc.config); got != tc.want {
				t.Fatalf("isInClusterCloudContext(%+v) = %v, want %v", tc.config, got, tc.want)
			}
		})
	}
}
