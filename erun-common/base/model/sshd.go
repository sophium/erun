package model

type SSHConnectionInfo struct {
	User           string
	Host           string
	Port           int
	WorkspacePath  string
	HostAlias      string
	PrivateKeyPath string
}
