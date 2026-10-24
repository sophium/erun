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

const runtimeIMDSEndpoint = "http://169.254.169.254"

// Where nothing answers the link-local address a request waits out the timeout, so keep it short.
const runtimeIMDSTimeout = 2 * time.Second

type runtimeCloudMetadata struct {
	region     string
	instanceID string
}

// runtimeEnvWithCloudMetadata fills an empty ERUN_CLOUD_REGION/ERUN_CLOUD_INSTANCE_ID from
// IMDS on AWS so idle stop and the Bedrock relay still work. Injected values always win.
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

// runtimeCloudProviderIsAWS must agree with the Claude settings relay's provider check.
func runtimeCloudProviderIsAWS(env func(string) string) bool {
	provider := strings.TrimSpace(env("ERUN_CLOUD_PROVIDER"))
	if provider == "" && strings.HasSuffix(strings.TrimSpace(env("ERUN_CLOUD_PROVIDER_ALIAS")), "@aws") {
		provider = "aws"
	}
	return provider == "aws"
}

// A rejected token request means IMDSv1, so plain reads follow; a timed-out one means
// nothing is listening, so no second timeout is paid.
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
