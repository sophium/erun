package model

// ObserveParams names the Secrets to check for key presence; everything else is always reported.
type ObserveParams struct {
	Secrets []ObserveSecretCheck
}

// ObserveSecretCheck names one Secret/key pair; the value is never read.
type ObserveSecretCheck struct {
	Name string
	Key  string
}

// ObserveResult is a read-only snapshot of an environment's Kubernetes state.
type ObserveResult struct {
	Tenant         string                  `json:"tenant"`
	Environment    string                  `json:"environment"`
	Namespace      string                  `json:"namespace"`
	Pods           []ObservedPod           `json:"pods"`
	ResourceQuotas []ObservedResourceQuota `json:"resourceQuotas"`
	LimitRanges    []ObservedLimitRange    `json:"limitRanges"`
	Ingresses      []ObservedIngress       `json:"ingresses"`
	Services       []ObservedService       `json:"services"`
	Certificates   []ObservedCertificate   `json:"certificates"`
	Secrets        []ObservedSecretCheck   `json:"secrets,omitempty"`
	// HelmRelease is nil in dry-run.
	HelmRelease *ObservedHelmRelease `json:"helmRelease,omitempty"`
	// Drift is not omitempty: [] means "compared, nothing disagreed"; a dry run's null means
	// "not determined".
	Drift []string `json:"drift"`
}
