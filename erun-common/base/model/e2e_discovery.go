package model

// E2ESuite is one suite under <tenant>-devops/e2e/<component>/; Component is
// also the exposed service it targets.
type E2ESuite struct {
	Component  string `json:"component"`
	Dir        string `json:"dir"`
	Dockerfile string `json:"dockerfile"`
}

// E2EPlan is the project root (every suite's build context) and the suites to run.
type E2EPlan struct {
	ProjectRoot string     `json:"projectRoot"`
	Suites      []E2ESuite `json:"suites"`
}
