package integration

// Idle, job, and lease verbs act on the local store inside the runtime and on
// the edge elsewhere, so each scenario states its half.

func inEnvironment(environment []string) []string {
	return append(append([]string{}, environment...), "ERUN_REPO_REMOTE=true")
}
