package cmd

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/manifoldco/promptui"
	common "github.com/sophium/erun/erun-common"
)

type cloudCommandStore struct {
	config common.ERunConfig
}

func (s *cloudCommandStore) LoadERunConfig() (common.ERunConfig, string, error) {
	return s.config, "", nil
}

func (s *cloudCommandStore) SaveERunConfig(config common.ERunConfig) error {
	s.config = config
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
		ResolveAWSIdentity: func(common.Context, string) (common.AWSIdentity, error) {
			return common.AWSIdentity{Account: "123456789012", Arn: "arn:aws:iam::123456789012:user/rihards"}, nil
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
		ResolveAWSIdentity: func(common.Context, string) (common.AWSIdentity, error) {
			return common.AWSIdentity{Account: "123456789012", Arn: "arn:aws:iam::123456789012:user/rihards"}, nil
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
