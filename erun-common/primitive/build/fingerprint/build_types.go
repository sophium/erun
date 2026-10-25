package fingerprint

type (
	DockerImageInspectorFunc func(string) (bool, error)
)
