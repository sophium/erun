package model

type JobListParams struct {
	Status        string
	EnvironmentID string
	IssueRef      string
	Scope         string
	ActorID       string
}

// JobClaimParams is the `erun jobs start` input; an empty Environment is host-side work.
type JobClaimParams struct {
	Environment string
	JobType     string
	IssueRef    string
	Summary     string
	// Status defaults to RUNNING.
	Status     string
	ActorKind  string
	ActorID    string
	Scope      string
	LocalJobID string
}

// JobUpdateParams moves an existing job forward; empty fields are unchanged.
type JobUpdateParams struct {
	JobID      string
	Status     string
	Summary    string
	LocalJobID string
}
