package model

// RemoteInitMarker records the intent of `erun init --remote` so doctor can finish it.
type RemoteInitMarker struct {
	Tenant             string `yaml:"tenant"`
	Environment        string `yaml:"environment"`
	ProjectRoot        string `yaml:"project_root"`
	RepositoryURL      string `yaml:"repository_url,omitempty"`
	CodeCommitHost     string `yaml:"codecommit_host,omitempty"`
	CodeCommitSSHKeyID string `yaml:"codecommit_ssh_key_id,omitempty"`
	NoGit              bool   `yaml:"no_git,omitempty"`
	BootstrapComplete  bool   `yaml:"bootstrap_complete"`
}
