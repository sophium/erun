package kube

// PortForwardStateKinds are every forward kind `erun open` records; deletion must clear all.
var PortForwardStateKinds = []string{"mcp", "api", "sshd"}
