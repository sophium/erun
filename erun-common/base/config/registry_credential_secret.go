package config

// DockerConfigJSONSecretKey is the auths-document key kubectl itself uses in a
// dockerconfigjson Secret (`kubectl create secret docker-registry`), shared by
// every Secret this package renders or reads back in that shape.
const DockerConfigJSONSecretKey = ".dockerconfigjson"

type DockerConfigJSONAuthEntry struct {
	Auth string `json:"auth"`
}

type DockerConfigJSONFile struct {
	Auths map[string]DockerConfigJSONAuthEntry `json:"auths"`
}
