package kubeapi

type (
	NamespaceDeleterFunc func(string, string) error
)
