package eruncommon

import (
	"errors"
	"testing"
)

type memoryCloudStore struct {
	config ERunConfig
	err    error
}

func (s *memoryCloudStore) LoadERunConfig() (ERunConfig, string, error) {
	if s.err != nil {
		return ERunConfig{}, "", s.err
	}
	return s.config, "", nil
}

func (s *memoryCloudStore) SaveERunConfig(config ERunConfig) error {
	s.config = config
	s.err = nil
	return nil
}

func TestInitAWSCloudProviderStoresAliasInRootConfig(t *testing.T) {
	store := &memoryCloudStore{err: ErrNotInitialized}
	loginCalled := false
	provider, err := InitAWSCloudProvider(Context{}, store, InitAWSCloudProviderParams{Profile: "dev"}, CloudDependencies{
		RunAWSLogin: func(Context, string) error {
			loginCalled = true
			return nil
		},
		ResolveAWSIdentity: func(Context, string) (AWSIdentity, error) {
			return AWSIdentity{
				Account: "123456789012",
				Arn:     "arn:aws:sts::123456789012:assumed-role/Admin/rihards",
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("InitAWSCloudProvider failed: %v", err)
	}
	if !loginCalled {
		t.Fatal("expected AWS login to run")
	}
	if provider.Alias != "rihards+123456789012@aws" {
		t.Fatalf("unexpected alias: %+v", provider)
	}
	if len(store.config.CloudProviders) != 1 || store.config.CloudProviders[0].Alias != provider.Alias {
		t.Fatalf("expected provider to be saved in root config, got %+v", store.config)
	}
}

func TestInitAWSCloudProviderCreatesProfileAndDerivesAliasFromLoginIdentity(t *testing.T) {
	store := &memoryCloudStore{err: ErrNotInitialized}
	var configuredProfile string
	var loginProfile string
	var identityProfile string
	provider, err := InitAWSCloudProvider(Context{}, store, InitAWSCloudProviderParams{
		SSOStartURL: "https://example.awsapps.com/start",
		SSORegion:   "eu-west-1",
		AccountID:   "123456789012",
		RoleName:    "Admin",
		Region:      "eu-west-1",
	}, CloudDependencies{
		RunAWSConfigureSSO: func(_ Context, config AWSProfileConfig) error {
			configuredProfile = config.Profile
			if config.SSOStartURL != "https://example.awsapps.com/start" || config.SSORegion != "eu-west-1" || config.AccountID != "123456789012" || config.RoleName != "Admin" || config.Region != "eu-west-1" {
				t.Fatalf("unexpected AWS profile config: %+v", config)
			}
			return nil
		},
		RunAWSLogin: func(_ Context, profile string) error {
			loginProfile = profile
			return nil
		},
		ResolveAWSIdentity: func(_ Context, profile string) (AWSIdentity, error) {
			identityProfile = profile
			return AWSIdentity{
				Account: "123456789012",
				Arn:     "arn:aws:sts::123456789012:assumed-role/Admin/rihards",
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("InitAWSCloudProvider failed: %v", err)
	}
	if configuredProfile == "" {
		t.Fatal("expected AWS SSO profile to be created")
	}
	if loginProfile != configuredProfile || identityProfile != configuredProfile || provider.Profile != configuredProfile {
		t.Fatalf("expected one generated profile through configure/login/identity/save, got configure=%q login=%q identity=%q provider=%q", configuredProfile, loginProfile, identityProfile, provider.Profile)
	}
	if provider.Alias != "rihards+123456789012@aws" {
		t.Fatalf("expected alias from identity, got %+v", provider)
	}
}

func TestLoginCloudProviderAliasRunsLoginWhenStatusIsExpired(t *testing.T) {
	store := &memoryCloudStore{config: ERunConfig{CloudProviders: []CloudProviderConfig{{
		Alias:    "rihards+123456789012@aws",
		Provider: CloudProviderAWS,
		Profile:  "dev",
	}}}}
	loginCalled := false
	checks := 0
	status, err := LoginCloudProviderAlias(Context{}, store, CloudLoginParams{Alias: "rihards+123456789012@aws"}, CloudDependencies{
		RunAWSLogin: func(Context, string) error {
			loginCalled = true
			return nil
		},
		CheckAWSStatus: func(_ Context, provider CloudProviderConfig) CloudProviderStatus {
			checks++
			if checks == 1 {
				return CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusExpired}
			}
			return CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusActive}
		},
	})
	if err != nil {
		t.Fatalf("LoginCloudProviderAlias failed: %v", err)
	}
	if !loginCalled {
		t.Fatal("expected AWS login to run")
	}
	if status.Status != CloudTokenStatusActive {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestListCloudProvidersTreatsMissingRootConfigAsEmpty(t *testing.T) {
	providers, err := ListCloudProviders(&memoryCloudStore{err: ErrNotInitialized})
	if err != nil {
		t.Fatalf("ListCloudProviders failed: %v", err)
	}
	if len(providers) != 0 {
		t.Fatalf("expected no providers, got %+v", providers)
	}
}

func TestResolveCloudProviderRequiresConfiguredAlias(t *testing.T) {
	_, err := ResolveCloudProvider(&memoryCloudStore{}, "missing@aws")
	if err == nil {
		t.Fatal("expected missing alias error")
	}
	if errors.Is(err, ErrNotInitialized) {
		t.Fatalf("expected missing alias error, got %v", err)
	}
}
