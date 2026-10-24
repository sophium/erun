package cloud

import (
	"errors"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// Without Force, an active session short-circuits login, so the desktop could never switch identity.
func TestLoginCloudProviderAliasForce(t *testing.T) {
	provider := model.CloudProviderConfig{Alias: "me+111111111111@aws", Provider: config.CloudProviderAWS, Profile: "me+111111111111@aws"}
	store := stubCloudContextStore{config: config.ERunConfig{CloudProviders: []model.CloudProviderConfig{provider}}}

	t.Run("without force, an active session short-circuits and never re-runs the login flow", func(t *testing.T) {
		loginCalled := false
		deps := CloudDependencies{
			CheckAWSStatus: func(core.Context, model.CloudProviderConfig) model.CloudProviderStatus {
				return model.CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusActive}
			},
			RunAWSLogin: func(core.Context, string) error {
				loginCalled = true
				return nil
			},
		}
		status, err := LoginCloudProviderAlias(core.Context{}, store, model.CloudLoginParams{Alias: provider.Alias}, deps)
		if err != nil {
			t.Fatalf("LoginCloudProviderAlias: %v", err)
		}
		if status.Status != config.CloudTokenStatusActive {
			t.Fatalf("Status = %q, want active", status.Status)
		}
		if loginCalled {
			t.Fatal("RunAWSLogin must not run while the session is already active and Force is false")
		}
	})

	t.Run("with force, an active session still re-runs the login flow", func(t *testing.T) {
		loginCalled := false
		deps := CloudDependencies{
			CheckAWSStatus: func(core.Context, model.CloudProviderConfig) model.CloudProviderStatus {
				return model.CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusActive}
			},
			RunAWSLogin: func(core.Context, string) error {
				loginCalled = true
				return nil
			},
		}
		status, err := LoginCloudProviderAlias(core.Context{}, store, model.CloudLoginParams{Alias: provider.Alias, Force: true}, deps)
		if err != nil {
			t.Fatalf("LoginCloudProviderAlias: %v", err)
		}
		if status.Status != config.CloudTokenStatusActive {
			t.Fatalf("Status = %q, want active", status.Status)
		}
		if !loginCalled {
			t.Fatal("RunAWSLogin must run when Force is true, even though the session was already active")
		}
	})

	t.Run("force does not delete anything up front, so a failed re-login leaves the prior session active", func(t *testing.T) {
		deps := CloudDependencies{
			CheckAWSStatus: func(core.Context, model.CloudProviderConfig) model.CloudProviderStatus {
				return model.CloudProviderStatus{CloudProviderConfig: provider, Status: config.CloudTokenStatusActive}
			},
			RunAWSLogin: func(core.Context, string) error {
				return errors.New("aws sso login: cancelled")
			},
		}
		_, err := LoginCloudProviderAlias(core.Context{}, store, model.CloudLoginParams{Alias: provider.Alias, Force: true}, deps)
		if err == nil {
			t.Fatal("expected the failed re-login to surface an error")
		}
		// A force re-login does not clear the prior session, so a failed attempt keeps the operator signed in.
		status := CloudProviderTokenStatus(provider, deps)
		if status.Status != config.CloudTokenStatusActive {
			t.Fatalf("Status after a failed switch = %q, want the prior session still active", status.Status)
		}
	})
}
