package eruncommon

import (
	"errors"
	"testing"
)

// TestLoginCloudProviderAliasForce locks in the contract the desktop's
// "sign in as someone else" action (erun-ui's SwitchCloudProviderIdentity)
// depends on: LoginCloudProviderAlias short-circuits without running any
// login flow at all when the stored session is already active, so switching
// accounts on an already-connected alias is impossible without Force -- not
// just impolite, structurally impossible, since the desktop has no other way
// to reach the provider's login flow.
func TestLoginCloudProviderAliasForce(t *testing.T) {
	provider := CloudProviderConfig{Alias: "me+111111111111@aws", Provider: CloudProviderAWS, Profile: "me+111111111111@aws"}
	store := stubCloudContextStore{config: ERunConfig{CloudProviders: []CloudProviderConfig{provider}}}

	t.Run("without force, an active session short-circuits and never re-runs the login flow", func(t *testing.T) {
		loginCalled := false
		deps := CloudDependencies{
			CheckAWSStatus: func(Context, CloudProviderConfig) CloudProviderStatus {
				return CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusActive}
			},
			RunAWSLogin: func(Context, string) error {
				loginCalled = true
				return nil
			},
		}
		status, err := LoginCloudProviderAlias(Context{}, store, CloudLoginParams{Alias: provider.Alias}, deps)
		if err != nil {
			t.Fatalf("LoginCloudProviderAlias: %v", err)
		}
		if status.Status != CloudTokenStatusActive {
			t.Fatalf("Status = %q, want active", status.Status)
		}
		if loginCalled {
			t.Fatal("RunAWSLogin must not run while the session is already active and Force is false")
		}
	})

	t.Run("with force, an active session still re-runs the login flow", func(t *testing.T) {
		loginCalled := false
		deps := CloudDependencies{
			CheckAWSStatus: func(Context, CloudProviderConfig) CloudProviderStatus {
				return CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusActive}
			},
			RunAWSLogin: func(Context, string) error {
				loginCalled = true
				return nil
			},
		}
		status, err := LoginCloudProviderAlias(Context{}, store, CloudLoginParams{Alias: provider.Alias, Force: true}, deps)
		if err != nil {
			t.Fatalf("LoginCloudProviderAlias: %v", err)
		}
		if status.Status != CloudTokenStatusActive {
			t.Fatalf("Status = %q, want active", status.Status)
		}
		if !loginCalled {
			t.Fatal("RunAWSLogin must run when Force is true, even though the session was already active")
		}
	})

	t.Run("force does not delete anything up front, so a failed re-login leaves the prior session active", func(t *testing.T) {
		deps := CloudDependencies{
			CheckAWSStatus: func(Context, CloudProviderConfig) CloudProviderStatus {
				return CloudProviderStatus{CloudProviderConfig: provider, Status: CloudTokenStatusActive}
			},
			RunAWSLogin: func(Context, string) error {
				return errors.New("aws sso login: cancelled")
			},
		}
		_, err := LoginCloudProviderAlias(Context{}, store, CloudLoginParams{Alias: provider.Alias, Force: true}, deps)
		if err == nil {
			t.Fatal("expected the failed re-login to surface an error")
		}
		// Switching identity is a force re-login, not a logout followed by a
		// login: LoginCloudProviderAlias never calls LogoutCloudProviderAlias
		// or otherwise clears the prior session before attempting the new one,
		// so a cancelled or failed attempt leaves the operator signed in as
		// they were, rather than signed out with nothing to show for it.
		status := CloudProviderTokenStatus(provider, deps)
		if status.Status != CloudTokenStatusActive {
			t.Fatalf("Status after a failed switch = %q, want the prior session still active", status.Status)
		}
	})
}
