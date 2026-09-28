package zitadel

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// machine.go provisions a tenant's *machine* identities: the service accounts
// an environment authenticates as, so its platform calls are attributable to
// the environment rather than to whichever operator ran `erun init` from a
// signed-in host.
//
// It provisions a service account -- Zitadel calls it a machine user -- rather
// than a project application, and that is not a preference. Zitadel's
// client_credentials grant resolves the presented client id as a user login
// name and then requires that user's machine secret, so a machine user is the
// only credential that grant can authenticate; a project application is not a
// user, so an identity minted as one is answered "client not found" however
// correct its client id and secret are. Zitadel's own service-account guide
// prescribes the same shape.
//
// A machine user lives in the organization, not in a project, so a tenant's
// machine identities are the org's own machine users, named one per
// environment.
//
// Everything here is find-first. The login name is the lookup key on the way
// back in, and it is derived from the environment's own id by the caller, so
// provisioning the same environment twice converges on the identity that
// already exists instead of minting a second one.

// machineIdentityDescription is the account's description, so an operator
// reading the organization in Zitadel's console can tell what created it.
const machineIdentityDescription = "erun environment machine identity"

// Zitadel's enum value for the one machine property that decides whether a
// token is usable at all.
//
// JWT rather than the opaque default is load-bearing, not cosmetic: an
// org-scoped issuer resolves its tenant from the claim named by the issuer's
// org field, and the erun-shipped Zitadel asserts that claim only on a JWT
// access token. A bearer-typed token authenticates at the IdP and then
// resolves to no tenant at the API, which answers 401 TENANT_UNRESOLVED with
// nothing in the IdP to point at.
const machineUserTokenTypeJWT = "ACCESS_TOKEN_TYPE_JWT"

// machineUserTypeMachine restricts the login-name lookup to machine users, so
// the derived name can never match a human account that happens to hold it.
const machineUserTypeMachine = "TYPE_MACHINE"

// tokenQueryMethodEquals is Zitadel's exact-text search method: the login name
// is derived and already unique, so a partial match would only widen what this
// can find.
const tokenQueryMethodEquals = "TEXT_QUERY_METHOD_EQUALS"

// MachineIdentity is one environment's own platform identity: the machine user
// a client_credentials grant authenticates as, together with the credential
// that grant presents.
//
// ClientID and Subject are two different values and both are needed. ClientID
// is the credential's public half -- the login name the grant presents as its
// Basic user. Subject is what a token minted from that credential carries as
// its own subject: Zitadel issues a service account's client_credentials token
// as the account's *user id*, not as its client id, so an erun user enrolled
// under the client id would never be resolved by any token the identity mints.
type MachineIdentity struct {
	ClientID     string
	ClientSecret string
	Subject      string
	// Created reports whether this call created the identity, as opposed to
	// finding one that already existed. It is what tells a caller that
	// anything it held under this name before this call belonged to an
	// identity that is not this one.
	Created bool
}

// EnsureMachineIdentityParams is the provisioning input for one environment's
// identity.
type EnsureMachineIdentityParams struct {
	// OrgID is the organization the identity belongs to. Empty acts in the
	// credential's own organization, the same convention the rest of this
	// client follows.
	OrgID string
	// LoginName identifies the identity within the organization and is this
	// call's idempotency key: it is what an existing machine user is looked up
	// by, so a caller must derive it deterministically from whatever the
	// identity is provisioned for.
	LoginName string
}

// EnsureMachineIdentity returns the machine identity for params.LoginName,
// creating it if it does not exist yet and converging it if it does.
//
// Idempotent by construction rather than by a caller-supplied flag: the
// machine user is looked up by its derived login name before anything is
// created, so a second call for one environment can only find what the first
// one made.
//
// The credential is not idempotent in the same way, and cannot be: Zitadel
// stores a machine secret as a hash and has no call that reads one back, so
// the only way to answer with a usable credential is to mint one. A repeat
// call therefore re-issues the identity's secret -- same machine user, same
// login name, same subject, new secret -- rather than returning the credential
// the first call handed out. What must not change on a repeat call is the
// identity, and it does not.
func (c *Client) EnsureMachineIdentity(ctx context.Context, params EnsureMachineIdentityParams) (MachineIdentity, error) {
	loginName := strings.TrimSpace(params.LoginName)
	if loginName == "" {
		return MachineIdentity{}, fmt.Errorf("a login name is required to provision a machine identity")
	}
	userID, err := c.findMachineIdentityUserID(ctx, params.OrgID, loginName)
	if err != nil {
		return MachineIdentity{}, err
	}
	created := userID == ""
	if created {
		userID, err = c.createMachineIdentityUser(ctx, params.OrgID, loginName)
		if err != nil {
			return MachineIdentity{}, err
		}
	}
	return c.mintMachineIdentitySecret(ctx, params.OrgID, userID, created)
}

// findMachineIdentityUserID answers which machine user in this organization
// holds loginName, without creating one. Revocation needs that distinction as
// much as provisioning does: provisioning asking "is there one" wants there to
// be, while revoking asking the same question must not conjure the very thing
// it was trying to find.
func (c *Client) findMachineIdentityUserID(ctx context.Context, orgID string, loginName string) (string, error) {
	var search struct {
		Result []struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := c.callInOrg(ctx, orgID, http.MethodPost, "/management/v1/users/_search", map[string]any{
		"queries": []map[string]any{
			{"userNameQuery": map[string]any{"userName": loginName, "method": tokenQueryMethodEquals}},
			{"typeQuery": map[string]any{"type": machineUserTypeMachine}},
		},
	}, &search); err != nil {
		return "", err
	}
	for _, user := range search.Result {
		if strings.TrimSpace(user.ID) != "" {
			return user.ID, nil
		}
	}
	return "", nil
}

// createMachineIdentityUser adds the machine user, returning its id. The
// display name is the login name: the two are shown side by side in Zitadel's
// console, and a second, prettier name would only be a second thing to keep in
// step with the environment a reader is matching it to.
func (c *Client) createMachineIdentityUser(ctx context.Context, orgID string, loginName string) (string, error) {
	var created struct {
		UserID string `json:"userId"`
	}
	if err := c.callInOrg(ctx, orgID, http.MethodPost, "/management/v1/users/machine", map[string]any{
		"userName":        loginName,
		"name":            loginName,
		"description":     machineIdentityDescription,
		"accessTokenType": machineUserTokenTypeJWT,
	}, &created); err != nil {
		return "", err
	}
	if strings.TrimSpace(created.UserID) == "" {
		return "", fmt.Errorf("zitadel created machine identity %q but returned no user id", loginName)
	}
	return created.UserID, nil
}

// mintMachineIdentitySecret issues the credential the client_credentials grant
// presents, and reports the identity in the three parts a caller needs.
//
// The client id the secret is minted against is the account's preferred login
// name, which under an instance whose domain policy requires login names to be
// domain-qualified is that name suffixed with the organization's own domain --
// so it is read back from the provider rather than reconstructed here. The
// subject is the account's own id, which is what a token minted from this
// credential carries and therefore what an erun user has to be enrolled under.
func (c *Client) mintMachineIdentitySecret(ctx context.Context, orgID string, userID string, created bool) (MachineIdentity, error) {
	var minted struct {
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
	}
	path := fmt.Sprintf("/management/v1/users/%s/secret", url.PathEscape(userID))
	if err := c.callInOrg(ctx, orgID, http.MethodPut, path, map[string]any{}, &minted); err != nil {
		return MachineIdentity{}, err
	}
	if strings.TrimSpace(minted.ClientID) == "" || strings.TrimSpace(minted.ClientSecret) == "" {
		return MachineIdentity{}, fmt.Errorf("zitadel minted no usable credential for machine identity %s", userID)
	}
	return MachineIdentity{
		ClientID:     minted.ClientID,
		ClientSecret: minted.ClientSecret,
		Subject:      userID,
		Created:      created,
	}, nil
}

// DeleteMachineIdentityParams is revocation's input for one environment's
// identity, and mirrors EnsureMachineIdentityParams: the same derived login
// name that provisioned the machine user is what finds it again.
type DeleteMachineIdentityParams struct {
	// OrgID is the organization the identity belongs to. Empty acts in the
	// credential's own organization, the same convention the rest of this
	// client follows.
	OrgID string
	// LoginName is the identity's name inside the organization, exactly as
	// passed to EnsureMachineIdentity.
	LoginName string
}

// DeleteMachineIdentity removes the machine identity named by
// params.LoginName, reporting whether there was one to remove.
//
// The whole account goes, not only its secret. Zitadel answers a deleted user
// with "client not found" on the client_credentials grant, and drops it from
// the login names it resolves by, so removing the account is what actually
// stops the credential -- where a secret removed on its own would leave an
// account that a later hand-generated secret could authenticate as again.
//
// Find-first, like everything else in this file, and for the same reason:
// "this identity does not exist" is the state revocation asks for, not a
// failure. An identity that was already revoked by an earlier attempt, never
// provisioned at all, or removed by hand is answered (false, nil) rather than
// as an error -- which is what makes revocation safe to re-run, and it has to
// be, because it runs inside the environment-delete workflow where an attempt
// that fails partway is retried from the top.
func (c *Client) DeleteMachineIdentity(ctx context.Context, params DeleteMachineIdentityParams) (bool, error) {
	loginName := strings.TrimSpace(params.LoginName)
	if loginName == "" {
		// Nothing derived no identity, so there is nothing to look for.
		return false, nil
	}
	userID, err := c.findMachineIdentityUserID(ctx, params.OrgID, loginName)
	if err != nil || userID == "" {
		return false, err
	}
	path := fmt.Sprintf("/management/v1/users/%s", url.PathEscape(userID))
	if err := c.callInOrg(ctx, params.OrgID, http.MethodDelete, path, nil, nil); err != nil {
		return false, err
	}
	return true, nil
}
