package podruntime

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// runtimeIMDSEndpoint is the EC2 instance metadata service as a pod on an EC2
// node reaches it.
const runtimeIMDSEndpoint = "http://169.254.169.254"

// runtimeIMDSTimeout bounds each metadata request. Where nothing answers the
// link-local address the request drains it, so it stays short.
const runtimeIMDSTimeout = 2 * time.Second

type runtimeCloudMetadata struct {
	region     string
	instanceID string
}

// runtimeEnvWithCloudMetadata fills ERUN_CLOUD_REGION and ERUN_CLOUD_INSTANCE_ID
// from the instance metadata service when the chart injected them empty on an
// AWS environment. The chart renders them from the deploying host's cloud
// context, and a deploy from a host that has none for the environment's kube
// context leaves them empty; without this the boot config would record no
// instance id, idle stop could never stop the host, and the Bedrock relay would
// be skipped. The lookup runs at most once, on first use; an injected value
// always wins and is never probed for.
func runtimeEnvWithCloudMetadata(env func(string) string, lookup func() runtimeCloudMetadata) func(string) string {
	var once sync.Once
	var metadata runtimeCloudMetadata
	return func(key string) string {
		value := env(key)
		if (key != "ERUN_CLOUD_REGION" && key != "ERUN_CLOUD_INSTANCE_ID") || strings.TrimSpace(value) != "" || !runtimeCloudProviderIsAWS(env) {
			return value
		}
		once.Do(func() { metadata = lookup() })
		if key == "ERUN_CLOUD_REGION" {
			return metadata.region
		}
		return metadata.instanceID
	}
}

// runtimeCloudProviderIsAWS reads the injected provider the way the Claude
// settings relay does: ERUN_CLOUD_PROVIDER, or an @aws provider alias.
func runtimeCloudProviderIsAWS(env func(string) string) bool {
	provider := strings.TrimSpace(env("ERUN_CLOUD_PROVIDER"))
	if provider == "" && strings.HasSuffix(strings.TrimSpace(env("ERUN_CLOUD_PROVIDER_ALIAS")), "@aws") {
		provider = "aws"
	}
	return provider == "aws"
}

// lookupRuntimeIMDS reads the region and instance id with IMDSv2. An instance
// that rejects the token request is IMDSv1-only and answers plain reads; a
// token request that timed out means nothing is listening, so the plain read
// is not tried and the boot does not pay a second timeout for an answer that
// cannot come. Every failure yields empty values.
func lookupRuntimeIMDS(endpoint string, timeout time.Duration) runtimeCloudMetadata {
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:       nil,
			DialContext: (&net.Dialer{Timeout: timeout / 2}).DialContext,
		},
	}
	defer client.CloseIdleConnections()
	token, err := runtimeIMDSToken(client, endpoint)
	var netErr net.Error
	if err != nil && errors.As(err, &netErr) && netErr.Timeout() {
		return runtimeCloudMetadata{}
	}
	get := func(path string) string {
		request, err := http.NewRequest(http.MethodGet, endpoint+"/latest/"+path, nil)
		if err != nil {
			return ""
		}
		if token != "" {
			request.Header.Set("X-aws-ec2-metadata-token", token)
		}
		return runtimeIMDSBody(client, request)
	}
	metadata := runtimeCloudMetadata{instanceID: get("meta-data/instance-id")}
	var document struct {
		Region string `json:"region"`
	}
	if json.Unmarshal([]byte(get("dynamic/instance-identity/document")), &document) == nil {
		metadata.region = strings.TrimSpace(document.Region)
	}
	return metadata
}

func runtimeIMDSToken(client *http.Client, endpoint string) (string, error) {
	request, err := http.NewRequest(http.MethodPut, endpoint+"/latest/api/token", nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("X-aws-ec2-metadata-token-ttl-seconds", "60")
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

func runtimeIMDSBody(client *http.Client, request *http.Request) string {
	response, err := client.Do(request)
	if err != nil {
		return ""
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 64*1024))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}
