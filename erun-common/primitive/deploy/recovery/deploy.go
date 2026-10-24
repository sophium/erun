package recovery

type HelmReleasePendingOperationError struct {
	ReleaseName       string
	Namespace         string
	KubernetesContext string
	// Tenant and Environment address `erun doctor --clear-pending-helm`; empty names only the kubectl command.
	Tenant      string
	Environment string
	Message     string
	Err         error
}
