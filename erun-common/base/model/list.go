package model

// ListOrchestratorResult is the `erun list` view of one OrchestratorConfig.
type ListOrchestratorResult struct {
	ID           string                      `json:"id"`
	Name         string                      `json:"name"`
	Environments []ListOrchestratorEnvResult `json:"environments,omitempty"`
	// Alias is the erun platform alias this orchestrator declares, if any.
	Alias string `json:"alias,omitempty"`
	// UseGateway, Model and Effort are as recorded, not resolved; empty means default.
	UseGateway bool   `json:"useGateway,omitempty"`
	Model      string `json:"model,omitempty"`
	Effort     string `json:"effort,omitempty"`
	// Directories are the orchestrator's own paths outside any environment.
	Directories []string `json:"directories,omitempty"`
}

// ListOrchestratorEnvResult is the `erun list` view of one OrchestratorEnvConfig link.
type ListOrchestratorEnvResult struct {
	Tenant      string              `json:"tenant"`
	Environment string              `json:"environment"`
	Directory   string              `json:"directory,omitempty"`
	Role        OrchestratorEnvRole `json:"role,omitempty"`
}

type ListDefaultsResult struct {
	Tenant      string `json:"tenant,omitempty"`
	Environment string `json:"environment,omitempty"`
}

// RuntimeImageLineMismatchResult reports a real recorded-vs-observed line disagreement.
type RuntimeImageLineMismatchResult struct {
	RecordedLine string `json:"recordedLine"`
	ObservedLine string `json:"observedLine"`
}

type ListSSHResult struct {
	Enabled                bool   `json:"enabled,omitempty"`
	HostAlias              string `json:"hostAlias,omitempty"`
	User                   string `json:"user,omitempty"`
	LocalPort              int    `json:"localPort,omitempty"`
	WorkspacePath          string `json:"workspacePath,omitempty"`
	PublicKeyPath          string `json:"publicKeyPath,omitempty"`
	WorkspaceSyncEnabled   bool   `json:"workspaceSyncEnabled,omitempty"`
	WorkspaceSyncLocalPath string `json:"workspaceSyncLocalPath,omitempty"`
}
