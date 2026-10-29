package model

// RegistryRole marks a registry's part in build → copy → deploy; an entry may carry several.
type RegistryRole string

const (
	RegistryRoleBuild RegistryRole = "build"
	RegistryRoleFrom  RegistryRole = "from"
	RegistryRoleTo    RegistryRole = "to"
	// RegistryRoleDeploy marks the registry the cluster pulls from.
	RegistryRoleDeploy RegistryRole = "deploy"
)
