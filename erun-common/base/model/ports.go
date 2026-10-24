package model

type EnvironmentLocalPorts struct {
	RangeStart    int `json:"rangeStart,omitempty"`
	RangeEnd      int `json:"rangeEnd,omitempty"`
	MCP           int `json:"mcp,omitempty"`
	API           int `json:"api,omitempty"`
	SSH           int `json:"ssh,omitempty"`
	ContributeApp int `json:"contributeApp,omitempty"`
}
