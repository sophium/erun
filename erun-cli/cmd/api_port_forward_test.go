package cmd

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	common "github.com/sophium/erun/erun-common"
)

func TestKubectlAPIPortForwardArgs(t *testing.T) {
	got := kubectlAPIPortForwardArgs(common.OpenResult{
		Tenant:      "tenant-a",
		Environment: "dev",
		EnvConfig: common.EnvConfig{
			KubernetesContext: "cluster-dev",
		},
		LocalPorts: common.EnvironmentLocalPorts{
			RangeStart: 17100,
			RangeEnd:   17199,
			MCP:        17100,
			API:        17133,
			SSH:        17122,
		},
	}, 17133)

	want := []string{
		"--context", "cluster-dev",
		"--namespace", "tenant-a-dev",
		"port-forward",
		"deployment/tenant-a-devops",
		"17133:17133",
		"--address", "127.0.0.1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected args:\ngot:  %v\nwant: %v", got, want)
	}
}

func TestCanReachLocalAPIEndpointUsesHealthz(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path = req.URL.Path
		if req.URL.Path != "/healthz" {
			http.NotFound(w, req)
			return
		}
		w.WriteHeader(http.StatusNoContent)
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
	if !canReachLocalAPIEndpoint(port) {
		t.Fatal("expected HTTP endpoint to be reachable")
	}
	if path != "/healthz" {
		t.Fatalf("unexpected readiness path: %q", path)
	}
}

func TestAPIPortForwardTimeoutDetailClassifiesRecentLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.log")
	if err := os.WriteFile(path, []byte(`failed to connect to localhost:17433, IPv4: dial tcp4 127.0.0.1:17433: connect: connection refused`), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	if got := apiPortForwardTimeoutDetail(path); got != "runtime pod exists but API is not accepting connections yet" {
		t.Fatalf("unexpected detail: %q", got)
	}
}
