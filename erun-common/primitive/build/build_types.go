package build

type (
	DockerImageInspectorFunc func(string) (bool, error)
)
