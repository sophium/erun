package envops

// Job times are unix seconds; zero means "not yet".

type EnvironmentJob struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	State     string   `json:"state"`
	Kind      string   `json:"kind,omitempty"`
	AgentTool string   `json:"agentTool,omitempty"`
	Command   []string `json:"command,omitempty"`
	Dir       string   `json:"dir,omitempty"`
	// ExitCode is nil unless exited, so a missing outcome never reads as success.
	ExitCode      *int                    `json:"exitCode"`
	StartedAtUnix int64                   `json:"startedAtUnix,omitempty"`
	EndedAtUnix   int64                   `json:"endedAtUnix,omitempty"`
	Progress      *EnvironmentJobProgress `json:"progress,omitempty"`
}

// EnvironmentJobProgress is nil for command jobs and agents that have not emitted.
type EnvironmentJobProgress struct {
	Activity    string `json:"activity,omitempty"`
	LastMessage string `json:"lastMessage,omitempty"`
	Turns       int    `json:"turns"`
	ToolsRun    int    `json:"toolsRun"`
}

type EnvironmentJobOutput struct {
	Job        EnvironmentJob `json:"job"`
	Offset     int64          `json:"offset"`
	NextOffset int64          `json:"nextOffset"`
	Output     string         `json:"output"`
	HasMore    bool           `json:"hasMore"`
	Complete   bool           `json:"complete"`
}

type JobOutputInput struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	ID          string `json:"id"`
	Offset      int64  `json:"offset"`
	MaxBytes    int64  `json:"maxBytes"`
}

type CancelJobInput struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	ID          string `json:"id"`
	// Signal is TERM, INT, HUP, or KILL; empty means TERM.
	Signal string `json:"signal,omitempty"`
}
