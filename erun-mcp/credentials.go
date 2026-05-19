package erunmcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// hostInjectedAWSProfileName is the section name written into the runtime
// pod's ~/.aws/credentials by InjectAWSCredentials. The deployment template
// sets AWS_PROFILE to this value when the env opts in to host-credential
// injection, so the AWS SDK in the pod re-reads the file on each call.
const hostInjectedAWSProfileName = "erun-host"

type InjectAWSCredentialsInput struct {
	AccessKeyID     string `json:"accessKeyId" jsonschema:"AWS access key id"`
	SecretAccessKey string `json:"secretAccessKey" jsonschema:"AWS secret access key"`
	SessionToken    string `json:"sessionToken,omitempty" jsonschema:"AWS session token; required for temporary credentials"`
	Expiration      string `json:"expiration,omitempty" jsonschema:"RFC3339 expiration timestamp; informational only"`
}

type InjectAWSCredentialsResult struct {
	Path    string `json:"path"`
	Profile string `json:"profile"`
}

type ClearAWSCredentialsInput struct{}

type ClearAWSCredentialsResult struct {
	Path    string `json:"path"`
	Profile string `json:"profile"`
	Removed bool   `json:"removed"`
}

func cloudInjectAWSCredentialsTool() func(context.Context, *mcp.CallToolRequest, InjectAWSCredentialsInput) (*mcp.CallToolResult, InjectAWSCredentialsResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input InjectAWSCredentialsInput) (*mcp.CallToolResult, InjectAWSCredentialsResult, error) {
		accessKey := strings.TrimSpace(input.AccessKeyID)
		secret := strings.TrimSpace(input.SecretAccessKey)
		if accessKey == "" || secret == "" {
			return nil, InjectAWSCredentialsResult{}, fmt.Errorf("accessKeyId and secretAccessKey are required")
		}
		path, err := awsCredentialsPath()
		if err != nil {
			return nil, InjectAWSCredentialsResult{}, err
		}
		profiles, err := readAWSCredentialsFile(path)
		if err != nil {
			return nil, InjectAWSCredentialsResult{}, err
		}
		profiles = setAWSCredentialProfile(profiles, hostInjectedAWSProfileName, awsCredentialEntries(input))
		if err := writeAWSCredentialsFile(path, profiles); err != nil {
			return nil, InjectAWSCredentialsResult{}, err
		}
		return nil, InjectAWSCredentialsResult{Path: path, Profile: hostInjectedAWSProfileName}, nil
	}
}

func cloudClearAWSCredentialsTool() func(context.Context, *mcp.CallToolRequest, ClearAWSCredentialsInput) (*mcp.CallToolResult, ClearAWSCredentialsResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, _ ClearAWSCredentialsInput) (*mcp.CallToolResult, ClearAWSCredentialsResult, error) {
		path, err := awsCredentialsPath()
		if err != nil {
			return nil, ClearAWSCredentialsResult{}, err
		}
		profiles, err := readAWSCredentialsFile(path)
		if err != nil {
			return nil, ClearAWSCredentialsResult{}, err
		}
		updated, removed := removeAWSCredentialProfile(profiles, hostInjectedAWSProfileName)
		if !removed {
			return nil, ClearAWSCredentialsResult{Path: path, Profile: hostInjectedAWSProfileName, Removed: false}, nil
		}
		if len(updated) == 0 {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return nil, ClearAWSCredentialsResult{}, err
			}
		} else if err := writeAWSCredentialsFile(path, updated); err != nil {
			return nil, ClearAWSCredentialsResult{}, err
		}
		return nil, ClearAWSCredentialsResult{Path: path, Profile: hostInjectedAWSProfileName, Removed: true}, nil
	}
}

func awsCredentialsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".aws", "credentials"), nil
}

func awsCredentialEntries(input InjectAWSCredentialsInput) []awsCredentialEntry {
	entries := []awsCredentialEntry{
		{Key: "aws_access_key_id", Value: strings.TrimSpace(input.AccessKeyID)},
		{Key: "aws_secret_access_key", Value: strings.TrimSpace(input.SecretAccessKey)},
	}
	if token := strings.TrimSpace(input.SessionToken); token != "" {
		entries = append(entries, awsCredentialEntry{Key: "aws_session_token", Value: token})
	}
	if expiration := strings.TrimSpace(input.Expiration); expiration != "" {
		entries = append(entries, awsCredentialEntry{Key: "x_erun_expiration", Value: expiration})
	}
	return entries
}
