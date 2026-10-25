package containerregistry

// DockerConfigJSONSecretKey is kubectl's dockerconfigjson Secret key.
const DockerConfigJSONSecretKey = ".dockerconfigjson"

type DockerConfigJSONAuthEntry struct {
	Auth string `json:"auth"`
}

type DockerConfigJSONFile struct {
	Auths map[string]DockerConfigJSONAuthEntry `json:"auths"`
}
