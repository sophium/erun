package deploy

type (
	NamespaceEnsurerFunc func(string, string) error
)
