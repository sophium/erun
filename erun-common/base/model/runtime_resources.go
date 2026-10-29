package model

type RuntimePodResources struct {
	CPU    string `yaml:"cpu,omitempty" json:"cpu,omitempty"`
	Memory string `yaml:"memory,omitempty" json:"memory,omitempty"`
}

// NamespaceResourceQuota is a hard per-namespace CPU/memory/storage cap. All
// three fields are set together: Kubernetes admits unbounded amounts of an
// uncapped resource.
type NamespaceResourceQuota struct {
	CPU     string `yaml:"cpu,omitempty" json:"cpu,omitempty"`
	Memory  string `yaml:"memory,omitempty" json:"memory,omitempty"`
	Storage string `yaml:"storage,omitempty" json:"storage,omitempty"`
}

// IsZero reports no cap, in which case deploy applies no ResourceQuota/LimitRange.
func (q NamespaceResourceQuota) IsZero() bool {
	return q == NamespaceResourceQuota{}
}
