package cmd

import (
	"reflect"
	"testing"

	common "github.com/sophium/erun/erun-common"
)

func TestKubectlMCPPortForwardArgs(t *testing.T) {
	got := kubectlMCPPortForwardArgs(common.OpenResult{
		Tenant:      "tenant-a",
		Environment: "dev",
		EnvConfig: common.EnvConfig{
			KubernetesContext: "cluster-dev",
		},
		LocalPorts: common.EnvironmentLocalPorts{
			RangeStart: 17100,
			RangeEnd:   17199,
			MCP:        17100,
			SSH:        17122,
		},
	}, 17100)

	want := []string{
		"--context", "cluster-dev",
		"--namespace", "tenant-a-dev",
		"port-forward",
		"deployment/tenant-a-devops",
		"17100:17000",
		"--address", "127.0.0.1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected args:\ngot:  %v\nwant: %v", got, want)
	}
}
