package model

type (
	DoctorAction string
)

const (
	DoctorActionPruneImages     DoctorAction = "prune_images"
	DoctorActionPruneBuildCache DoctorAction = "prune_build_cache"
	DoctorActionPruneContainers DoctorAction = "prune_containers"
)
