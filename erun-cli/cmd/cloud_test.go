package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/manifoldco/promptui"
	common "github.com/sophium/erun/erun-common"
)

type cloudCommandStore struct {
	config common.ERunConfig
	envs   map[string]common.EnvConfig
}

func (s *cloudCommandStore) LoadERunConfig() (common.ERunConfig, string, error) {
	return s.config, "", nil
}

func (s *cloudCommandStore) SaveERunConfig(config common.ERunConfig) error {
	s.config = config
	return nil
}

func (s *cloudCommandStore) LoadEnvConfig(tenant, environment string) (common.EnvConfig, string, error) {
	config, ok := s.envs[tenant+"/"+environment]
	if !ok {
		return common.EnvConfig{}, "", common.ErrNotInitialized
	}
	return config, "", nil
}

func (s *cloudCommandStore) SaveEnvConfig(tenant string, config common.EnvConfig) error {
	if s.envs == nil {
		s.envs = make(map[string]common.EnvConfig)
	}
	s.envs[tenant+"/"+config.Name] = config
	return nil
}

func TestRunCloudInitAWSCommandStoresAlias(t *testing.T) {
	store := &cloudCommandStore{}
	stdout := new(bytes.Buffer)
	loginCalled := false
	err := runCloudInitAWSCommand(common.Context{Stdout: stdout}, store, func(prompt promptui.Prompt) (string, error) {
		return "y", nil
	}, common.InitAWSCloudProviderParams{Profile: "dev"}, common.CloudDependencies{
		RunAWSLogin: func(common.Context, string) error {
			loginCalled = true
			return nil
		},
		RunAWSBearerToken: func(common.Context, string, string) (string, error) {
			return testCloudJWT(t, "https://sts.aws.example"), nil
		},
		ResolveAWSIdentity: func(common.Context, string) (common.AWSIdentity, error) {
			return common.AWSIdentity{Account: "123456789012", Arn: "arn:aws:iam::123456789012:user/rihards"}, nil
		},
		CheckAWSStatus: func(_ common.Context, provider common.CloudProviderConfig) common.CloudProviderStatus {
			return common.CloudProviderStatus{CloudProviderConfig: provider, Status: common.CloudTokenStatusActive}
		},
	})
	if err != nil {
		t.Fatalf("runCloudInitAWSCommand failed: %v", err)
	}
	if !loginCalled {
		t.Fatal("expected AWS login to run")
	}
	if len(store.config.CloudProviders) != 1 || store.config.CloudProviders[0].Alias != "rihards+123456789012@aws" {
		t.Fatalf("unexpected stored config: %+v", store.config)
	}
	if got := stdout.String(); got != "Saved cloud provider alias rihards+123456789012@aws\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}
}

func TestRunCloudInitAWSCommandCreatesProfileWithoutPromptingForAlias(t *testing.T) {
	store := &cloudCommandStore{}
	stdout := new(bytes.Buffer)
	var configuredProfile string
	prompts := map[string]string{
		"AWS SSO start URL":  "https://example.awsapps.com/start",
		"AWS SSO region":     "eu-west-1",
		"AWS account ID":     "123456789012",
		"AWS role name":      "Admin",
		"Default AWS region": "eu-west-1",
	}
	err := runCloudInitAWSCommand(common.Context{Stdout: stdout}, store, func(prompt promptui.Prompt) (string, error) {
		return prompts[fmt.Sprint(prompt.Label)], nil
	}, common.InitAWSCloudProviderParams{}, common.CloudDependencies{
		RunAWSConfigureSSO: func(_ common.Context, config common.AWSProfileConfig) error {
			configuredProfile = config.Profile
			if config.SSOStartURL != "https://example.awsapps.com/start" || config.SSORegion != "eu-west-1" || config.AccountID != "123456789012" || config.RoleName != "Admin" || config.Region != "eu-west-1" {
				t.Fatalf("unexpected AWS profile config: %+v", config)
			}
			return nil
		},
		RunAWSLogin: func(common.Context, string) error {
			return nil
		},
		RunAWSBearerToken: func(common.Context, string, string) (string, error) {
			return testCloudJWT(t, "https://sts.aws.example"), nil
		},
		ResolveAWSIdentity: func(common.Context, string) (common.AWSIdentity, error) {
			return common.AWSIdentity{Account: "123456789012", Arn: "arn:aws:iam::123456789012:user/rihards"}, nil
		},
		CheckAWSStatus: func(_ common.Context, provider common.CloudProviderConfig) common.CloudProviderStatus {
			return common.CloudProviderStatus{CloudProviderConfig: provider, Status: common.CloudTokenStatusActive}
		},
	})
	if err != nil {
		t.Fatalf("runCloudInitAWSCommand failed: %v", err)
	}
	if configuredProfile == "" {
		t.Fatal("expected command to create an AWS SSO profile")
	}
	if len(store.config.CloudProviders) != 1 || store.config.CloudProviders[0].Alias != "rihards+123456789012@aws" {
		t.Fatalf("unexpected stored config: %+v", store.config)
	}
}

func TestRunCloudLoginCommandPromptsWhenExpired(t *testing.T) {
	store := &cloudCommandStore{config: common.ERunConfig{CloudProviders: []common.CloudProviderConfig{{
		Alias:    "rihards+123456789012@aws",
		Provider: common.CloudProviderAWS,
		Profile:  "dev",
	}}}}
	stdout := new(bytes.Buffer)
	loginCalled := false
	checks := 0
	err := runCloudLoginCommand(common.Context{Stdout: stdout}, store, func(promptui.Prompt) (string, error) {
		return "y", nil
	}, nil, common.CloudLoginParams{Alias: "rihards+123456789012@aws"}, common.CloudDependencies{
		RunAWSLogin: func(common.Context, string) error {
			loginCalled = true
			return nil
		},
		CheckAWSStatus: func(_ common.Context, provider common.CloudProviderConfig) common.CloudProviderStatus {
			checks++
			if checks == 1 {
				return common.CloudProviderStatus{CloudProviderConfig: provider, Status: common.CloudTokenStatusExpired}
			}
			return common.CloudProviderStatus{CloudProviderConfig: provider, Status: common.CloudTokenStatusActive}
		},
	})
	if err != nil {
		t.Fatalf("runCloudLoginCommand failed: %v", err)
	}
	if !loginCalled {
		t.Fatal("expected AWS login to run")
	}
	if got := stdout.String(); got != "rihards+123456789012@aws: active\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}
}

func TestRunCloudOIDCCommandStoresIssuer(t *testing.T) {
	store := &cloudCommandStore{config: common.ERunConfig{CloudProviders: []common.CloudProviderConfig{{
		Alias:    "rihards+123456789012@aws",
		Provider: common.CloudProviderAWS,
		Profile:  "dev",
	}}}}
	stdout := new(bytes.Buffer)

	err := runCloudOIDCCommand(common.Context{Stdout: stdout}, store, nil, nil, common.CloudBearerParams{
		Alias:    "rihards+123456789012@aws",
		Audience: "https://api.example",
	}, common.CloudDependencies{
		RunAWSBearerToken: func(_ common.Context, profile, audience string) (string, error) {
			if profile != "dev" || audience != "https://api.example" {
				t.Fatalf("unexpected bearer token input profile=%q audience=%q", profile, audience)
			}
			return testCloudJWT(t, "https://sts.aws.example/"), nil
		},
		CheckAWSStatus: func(_ common.Context, provider common.CloudProviderConfig) common.CloudProviderStatus {
			return common.CloudProviderStatus{CloudProviderConfig: provider, Status: common.CloudTokenStatusActive}
		},
	})
	if err != nil {
		t.Fatalf("runCloudOIDCCommand failed: %v", err)
	}
	if store.config.CloudProviders[0].OIDCIssuerURL != "https://sts.aws.example" {
		t.Fatalf("unexpected stored issuer: %+v", store.config.CloudProviders[0])
	}
	if got := stdout.String(); got != "Saved OIDC issuer https://sts.aws.example for rihards+123456789012@aws\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}
}

func TestRunCloudSetCommandSetsEnvironmentAlias(t *testing.T) {
	store := &cloudCommandStore{envs: map[string]common.EnvConfig{
		"frs/dev": {
			Name:               "dev",
			RepoPath:           "/workspace/frs",
			KubernetesContext:  "cluster-dev",
			CloudProviderAlias: "old-cloud",
			Remote:             true,
		},
	}}
	stdout := new(bytes.Buffer)

	err := runCloudSetCommand(common.Context{Stdout: stdout}, store, common.SetEnvironmentCloudAliasParams{
		Tenant:      "frs",
		Environment: "dev",
		Alias:       "team-cloud",
	})
	if err != nil {
		t.Fatalf("runCloudSetCommand failed: %v", err)
	}
	if store.envs["frs/dev"].CloudProviderAlias != "team-cloud" {
		t.Fatalf("unexpected stored env: %+v", store.envs["frs/dev"])
	}
	if got := stdout.String(); got != "Set cloud provider alias team-cloud for frs/dev\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}
}

func testCloudJWT(t *testing.T, issuer string) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		t.Fatalf("marshal JWT header: %v", err)
	}
	payload, err := json.Marshal(map[string]string{"iss": issuer})
	if err != nil {
		t.Fatalf("marshal JWT payload: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}
