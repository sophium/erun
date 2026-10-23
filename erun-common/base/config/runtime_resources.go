package config

type RuntimePodResources struct {
	CPU    string `yaml:"cpu,omitempty" json:"cpu,omitempty"`
	Memory string `yaml:"memory,omitempty" json:"memory,omitempty"`
}

// NamespaceResourceQuota is a hard per-environment-namespace ceiling on
// CPU/memory/storage, enforced by Kubernetes via a ResourceQuota + LimitRange
// applied to the namespace at deploy time (kubernetes_resource_quota.go). It is
// distinct from RuntimePodResources: RuntimePodResources sizes the runtime
// pod's own container; NamespaceResourceQuota caps everything that namespace
// can ever hold, including future non-runtime workloads. All three fields must
// be set together — a namespace ResourceQuota is meaningless with only some
// resources capped, since Kubernetes then admits unbounded amounts of the
// uncapped ones.
type NamespaceResourceQuota struct {
	CPU     string `yaml:"cpu,omitempty" json:"cpu,omitempty"`
	Memory  string `yaml:"memory,omitempty" json:"memory,omitempty"`
	Storage string `yaml:"storage,omitempty" json:"storage,omitempty"`
}

// IsZero reports whether no cap was configured, so deploy applies no
// ResourceQuota/LimitRange at all rather than an incomplete one.
func (q NamespaceResourceQuota) IsZero() bool {
	return q == NamespaceResourceQuota{}
}
