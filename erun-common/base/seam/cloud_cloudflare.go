package seam

// CloudSecretStore keeps provider secrets outside the config, which carries only a ref.
type CloudSecretStore interface {
	SaveCloudSecret(ref, value string) error
	LoadCloudSecret(ref string) (string, error)
	DeleteCloudSecret(ref string) error
}
