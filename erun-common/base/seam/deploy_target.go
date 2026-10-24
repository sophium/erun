package seam

type BuildDeployStore interface {
	DeployStore
	DockerStore
}
