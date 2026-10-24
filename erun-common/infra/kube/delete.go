package kube

// PortForwardStateKinds are every kind of forward `erun open` can record for
// one environment. Deleting the environment must clear all three, or whichever
// one is skipped outlives the environment it describes.
var PortForwardStateKinds = []string{"mcp", "api", "sshd"}
