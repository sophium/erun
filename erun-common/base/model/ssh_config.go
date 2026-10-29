package model

type SSHHostEntry struct {
	Alias        string
	HostKeyAlias string
	HostName     string
	Port         int
	User         string
	IdentityFile string
}
