package kubeapi

type (
	NamespaceEnsurerFunc func(string, string) error
)
