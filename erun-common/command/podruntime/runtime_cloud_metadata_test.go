package podruntime

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/runtimesync"
	"github.com/sophium/erun/erun-common/base/core"
)

func fakeIMDS(t *testing.T, v2 bool, requests *atomic.Int32) string {
	t.Helper()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/latest/api/token" {
			if !v2 || r.Method != http.MethodPut {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = w.Write([]byte("tok"))
			return
		}
		if v2 && r.Header.Get("X-aws-ec2-metadata-token") != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/latest/meta-data/instance-id":
			_, _ = w.Write([]byte("i-0abc123\n"))
		case "/latest/dynamic/instance-identity/document":
			_, _ = w.Write([]byte(`{"instanceId":"i-0abc123","region":"eu-west-2"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	server.Start()
	t.Cleanup(server.Close)
	return server.URL
}

func TestRuntimeBootFillsAnUninjectedRegionAndInstanceIDFromIMDS(t *testing.T) {
	for _, v2 := range []bool{true, false} {
		var requests atomic.Int32
		endpoint := fakeIMDS(t, v2, &requests)
		boot := newTestRuntimeBoot(t, map[string]string{
			"ERUN_TENANT": "team", "ERUN_ENVIRONMENT": "dev",
			"ERUN_CLOUD_PROVIDER": "aws", "ERUN_CLOUD_PROVIDER_ALIAS": "ops+123456789012@aws",
		})
		boot.env = runtimeEnvWithCloudMetadata(boot.env, func() runtimeCloudMetadata { return lookupRuntimeIMDS(endpoint, time.Second) })
		if got := boot.get("ERUN_CLOUD_INSTANCE_ID"); got != "i-0abc123" {
			t.Fatalf("v2=%v: instance id = %q, want it read from IMDS", v2, got)
		}
		if got := boot.get("ERUN_CLOUD_REGION"); got != "eu-west-2" {
			t.Fatalf("v2=%v: region = %q, want it read from IMDS", v2, got)
		}
		seen := requests.Load()
		_ = boot.get("ERUN_CLOUD_REGION")
		if requests.Load() != seen {
			t.Fatalf("v2=%v: the metadata must be read once per boot", v2)
		}
		if err := writeRuntimeBootConfig(core.Context{}, boot, nil); err != nil {
			t.Fatal(err)
		}
		var root config.ERunConfig
		readTestYAML(t, runtimesync.RuntimeRootConfigPath(boot.configHome()), &root)
		if len(root.CloudContexts) != 1 || root.CloudContexts[0].InstanceID != "i-0abc123" || root.CloudContexts[0].Region != "eu-west-2" {
			t.Fatalf("v2=%v: the boot config should record the instance's cloud context, got %+v", v2, root.CloudContexts)
		}
	}
}

func TestRuntimeBootProbesIMDSOnlyForAnUninjectedValueOnAWS(t *testing.T) {
	var requests atomic.Int32
	endpoint := fakeIMDS(t, true, &requests)
	lookup := func() runtimeCloudMetadata { return lookupRuntimeIMDS(endpoint, time.Second) }
	injected := runtimeEnvWithCloudMetadata(func(key string) string {
		return map[string]string{"ERUN_CLOUD_PROVIDER": "aws", "ERUN_CLOUD_REGION": "us-east-1", "ERUN_CLOUD_INSTANCE_ID": "i-injected"}[key]
	}, lookup)
	if injected("ERUN_CLOUD_REGION") != "us-east-1" || injected("ERUN_CLOUD_INSTANCE_ID") != "i-injected" {
		t.Fatal("injected values must win")
	}
	other := runtimeEnvWithCloudMetadata(func(key string) string {
		return map[string]string{"ERUN_CLOUD_PROVIDER": "gcp"}[key]
	}, lookup)
	if other("ERUN_CLOUD_INSTANCE_ID") != "" {
		t.Fatal("a non-AWS environment must not read EC2 metadata")
	}
	if requests.Load() != 0 {
		t.Fatalf("no metadata request should have been made, saw %d", requests.Load())
	}
}

func TestRuntimeIMDSTimeoutDoesNotFallBackToAPlainRead(t *testing.T) {
	var reads atomic.Int32
	release := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest/api/token" {
			<-release
			return
		}
		reads.Add(1)
	}))
	server.Start()
	t.Cleanup(func() { close(release); server.Close() })
	if got := lookupRuntimeIMDS(server.URL, 200*time.Millisecond); got != (runtimeCloudMetadata{}) {
		t.Fatalf("a timed-out lookup yields nothing, got %+v", got)
	}
	if reads.Load() != 0 {
		t.Fatalf("a timed-out token request must not fall back to plain reads, saw %d", reads.Load())
	}
}
