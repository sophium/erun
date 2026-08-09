package integration

// The idle, job, and activity-lease verbs have two halves. Inside the
// environment's runtime they act on its own activity store; anywhere else they
// act on the environment through its MCP edge. A scenario has to say which half
// it is exercising, because the same argv means different things in each and a
// scenario that did not say would record whichever half the machine running it
// happened to be.

// inEnvironment marks a scenario as running inside the environment's runtime.
func inEnvironment(environment []string) []string {
	return append(append([]string{}, environment...), "ERUN_REPO_REMOTE=true")
}
