package cmd

import (
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
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
		"17100:17100",
		"--address", "127.0.0.1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected args:\ngot:  %v\nwant: %v", got, want)
	}
}

func TestCanReachLocalMCPEndpointRequiresHTTPResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Bad Request", http.StatusBadRequest)
	}))
	defer server.Close()

	host, portValue, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split address: %v", err)
	}
	if host == "" {
		t.Fatal("expected listener host")
	}
	port, err := strconv.Atoi(portValue)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	if !canReachLocalMCPEndpoint(port) {
		t.Fatal("expected HTTP endpoint to be reachable")
	}
}
