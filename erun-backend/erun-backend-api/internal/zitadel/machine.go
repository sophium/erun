package zitadel

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// machine.go provisions a tenant's *machine* identities: the client-credentials
// applications an environment authenticates as, so its platform calls are
// attributable to the environment rather than to whichever operator ran
// `erun init` from a signed-in host.
//
// One project per organization and one application per environment inside it.
// A project per environment would multiply the objects an operator has to read
// in Zitadel's console for no gain; the application is the identity, and its
// client id is what a token's subject carries, so that is the level that has
// to be distinct per environment.
//
// Everything here is find-first. The login name is the lookup key on the way
// back in, and it is derived from the environment's own id by the caller, so
// provisioning the same environment twice converges on the identity that
// already exists instead of minting a second one.

// machineIdentityProjectName is the project every machine identity lives in,
// created in the organization on first use. Deliberately not the project the
// platform's own OIDC applications live in: those are the platform's sign-in
// clients, shared by every tenant, and a per-tenant service identity has no
// business among them.
const machineIdentityProjectName = "erun-agents"

// Zitadel's enum values for the two application properties that decide whether
// a machine token is usable at all.
//
// JWT rather than the opaque default is load-bearing, not cosmetic: an
// org-scoped issuer resolves its tenant from the claim named by the issuer's
// org field, and the erun-shipped Zitadel asserts that claim only on a JWT
// access token. A bearer-typed token authenticates at the IdP and then
// resolves to no tenant at the API, which answers 401 TENANT_UNRESOLVED with
// nothing in the IdP to point at.
const (
	apiAuthMethodBasic = "API_AUTH_METHOD_TYPE_BASIC"
	apiTokenTypeJWT    = "API_TOKEN_TYPE_JWT"
)

// machineIdentityAppDescription is the application's description, so an
// operator reading the project in Zitadel's console can tell what created it.
const machineIdentityAppDescription = "erun environment machine identity"

// MachineIdentity is one environment's own platform identity: the Zitadel
// application a client_credentials grant authenticates as, together with the
// credential that grant presents.
//
// ClientID is both halves at once — the credential's public half, and the
// value a token minted from it carries as its subject (Zitadel's
// client_credentials grant issues as the application, not as a separate user
// object). That is why the caller enrols erun's user row under ClientID: it is
// what a later token will actually resolve by.
type MachineIdentity struct {
	ClientID     string
	ClientSecret string
}

// EnsureMachineIdentityParams is the provisioning input for one environment's
// identity.
type EnsureMachineIdentityParams struct {
	// OrgID is the organization the identity belongs to. Empty acts in the
	// credential's own organization, the same convention the rest of this
	// client follows.
	OrgID string
	// LoginName identifies the identity within the project and is this call's
	// idempotency key: it is what an existing application is looked up by, so
	// a caller must derive it deterministically from whatever the identity is
	// provisioned for.
	LoginName string
}

// EnsureMachineIdentity returns the machine identity for params.LoginName,
// creating it if it does not exist yet and converging it if it does.
//
// Idempotent by construction rather than by a caller-supplied flag: the
// project is found or created, the application is looked up by name before
// anything is created, and an application that already exists is returned as
// it stands (with its access token type corrected if a hand-created one was
// left on the opaque default).
func (c *Client) EnsureMachineIdentity(ctx context.Context, params EnsureMachineIdentityParams) (MachineIdentity, error) {
	loginName := strings.TrimSpace(params.LoginName)
	if loginName == "" {
		return MachineIdentity{}, fmt.Errorf("a login name is required to provision a machine identity")
	}
	projectID, err := c.ensureMachineIdentityProject(ctx, params.OrgID)
	if err != nil {
		return MachineIdentity{}, err
	}
	appID, err := c.findMachineIdentityAppID(ctx, params.OrgID, projectID, loginName)
	if err != nil {
		return MachineIdentity{}, err
	}
	if appID == "" {
		return c.createMachineIdentityApp(ctx, params.OrgID, projectID, loginName)
	}
	return c.convergeMachineIdentityApp(ctx, params.OrgID, projectID, appID)
}

type zitadelProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (c *Client) ensureMachineIdentityProject(ctx context.Context, orgID string) (string, error) {
	projectID, err := c.findMachineIdentityProjectID(ctx, orgID)
	if err != nil {
		return "", err
	}
	if projectID != "" {
		return projectID, nil
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := c.callInOrg(ctx, orgID, http.MethodPost, "/management/v1/projects", map[string]any{
		"name": machineIdentityProjectName,
	}, &created); err != nil {
		return "", err
	}
	if strings.TrimSpace(created.ID) == "" {
		return "", fmt.Errorf("zitadel created project %q but returned no id", machineIdentityProjectName)
	}
	return created.ID, nil
}

// findMachineIdentityProjectID is the read-only half of
// ensureMachineIdentityProject: it answers where the machine-identity project
// is, without creating one. Revocation needs that distinction — provisioning
// asking "where is the project" wants one to exist, while revoking asking the
// same question must not conjure the very thing it was trying to find.
func (c *Client) findMachineIdentityProjectID(ctx context.Context, orgID string) (string, error) {
	var search struct {
		Result []zitadelProject `json:"result"`
	}
	if err := c.callInOrg(ctx, orgID, http.MethodPost, "/management/v1/projects/_search", map[string]any{}, &search); err != nil {
		return "", err
	}
	for _, project := range search.Result {
		if project.Name == machineIdentityProjectName {
			return project.ID, nil
		}
	}
	return "", nil
}

func (c *Client) findMachineIdentityAppID(ctx context.Context, orgID string, projectID string, loginName string) (string, error) {
	var search struct {
		Result []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"result"`
	}
	path := fmt.Sprintf("/management/v1/projects/%s/apps/_search", url.PathEscape(projectID))
	if err := c.callInOrg(ctx, orgID, http.MethodPost, path, map[string]any{}, &search); err != nil {
		return "", err
	}
	for _, app := range search.Result {
		if app.Name == loginName {
			return app.ID, nil
		}
	}
	return "", nil
}

// apiAppConfig is the application read shape. apiConfig is absent for an OIDC
// application, the same way oidcConfig is absent for this one; a machine
// identity must therefore have been found under a name that only a machine
// identity could hold, or the empty client id below is reported rather than
// silently enrolled as "".
type apiAppConfig struct {
	ClientID       string `json:"clientId"`
	ClientSecret   string `json:"clientSecret"`
	AccessTokenTyp string `json:"accessTokenType"`
}

type apiAppResponse struct {
	App struct {
		ID        string        `json:"id"`
		Name      string        `json:"name"`
		APIConfig *apiAppConfig `json:"apiConfig"`
	} `json:"app"`
}

func (c *Client) createMachineIdentityApp(ctx context.Context, orgID string, projectID string, loginName string) (MachineIdentity, error) {
	var created struct {
		AppID        string `json:"appId"`
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
	}
	path := fmt.Sprintf("/management/v1/projects/%s/apps/api", url.PathEscape(projectID))
	if err := c.callInOrg(ctx, orgID, http.MethodPost, path, map[string]any{
		"name":            loginName,
		"description":     machineIdentityAppDescription,
		"authMethodType":  apiAuthMethodBasic,
		"accessTokenType": apiTokenTypeJWT,
	}, &created); err != nil {
		return MachineIdentity{}, err
	}
	if strings.TrimSpace(created.ClientID) == "" || strings.TrimSpace(created.ClientSecret) == "" {
		return MachineIdentity{}, fmt.Errorf("zitadel created machine identity %q but returned no client credentials", loginName)
	}
	return MachineIdentity{ClientID: created.ClientID, ClientSecret: created.ClientSecret}, nil
}

// convergeMachineIdentityApp reads an application that already exists and
// corrects the one property that silently makes it useless: an access token
// type other than JWT leaves every token it mints unable to resolve a tenant.
// The correction is what makes this a reconcile rather than a lookup — a
// hand-created application is otherwise indistinguishable from a provisioned
// one until its first call fails.
func (c *Client) convergeMachineIdentityApp(ctx context.Context, orgID string, projectID string, appID string) (MachineIdentity, error) {
	var response apiAppResponse
	path := fmt.Sprintf("/management/v1/projects/%s/apps/%s", url.PathEscape(projectID), url.PathEscape(appID))
	if err := c.callInOrg(ctx, orgID, http.MethodGet, path, nil, &response); err != nil {
		return MachineIdentity{}, err
	}
	config := response.App.APIConfig
	if config == nil {
		return MachineIdentity{}, fmt.Errorf("zitadel application %s is not an api application", appID)
	}
	if config.AccessTokenTyp != apiTokenTypeJWT {
		updatePath := fmt.Sprintf("/management/v1/projects/%s/apps/%s/api_config", url.PathEscape(projectID), url.PathEscape(appID))
		// The update replaces the whole config rather than merging, so the auth
		// method the application was created with is carried back over.
		if err := c.callInOrg(ctx, orgID, http.MethodPut, updatePath, map[string]any{
			"authMethodType":  apiAuthMethodBasic,
			"accessTokenType": apiTokenTypeJWT,
		}, nil); err != nil {
			return MachineIdentity{}, err
		}
	}
	if strings.TrimSpace(config.ClientID) == "" || strings.TrimSpace(config.ClientSecret) == "" {
		return MachineIdentity{}, fmt.Errorf("zitadel application %s reports no client credentials", appID)
	}
	return MachineIdentity{ClientID: config.ClientID, ClientSecret: config.ClientSecret}, nil
}

// DeleteMachineIdentityParams is revocation's input for one environment's
// identity, and mirrors EnsureMachineIdentityParams: the same derived login
// name that provisioned the application is what finds it again.
type DeleteMachineIdentityParams struct {
	// OrgID is the organization the identity belongs to. Empty acts in the
	// credential's own organization, the same convention the rest of this
	// client follows.
	OrgID string
	// LoginName is the identity's name inside the project, exactly as passed
	// to EnsureMachineIdentity.
	LoginName string
}

// DeleteMachineIdentity removes the machine identity named by
// params.LoginName, reporting whether there was one to remove.
//
// Find-first, like everything else in this file, and for the same reason:
// "this identity does not exist" is the state revocation asks for, not a
// failure. An identity that was already revoked by an earlier attempt, never
// provisioned at all, or removed by hand is answered (false, nil) rather than
// as an error — which is what makes revocation safe to re-run, and it has to
// be, because it runs inside the environment-delete workflow where an attempt
// that fails partway is retried from the top.
//
// The project is searched for, never created: a delete that conjures the thing
// it was trying to find is worse than one that finds nothing.
func (c *Client) DeleteMachineIdentity(ctx context.Context, params DeleteMachineIdentityParams) (bool, error) {
	loginName := strings.TrimSpace(params.LoginName)
	if loginName == "" {
		// Nothing derived no identity, so there is nothing to look for.
		return false, nil
	}
	projectID, err := c.findMachineIdentityProjectID(ctx, params.OrgID)
	if err != nil || projectID == "" {
		return false, err
	}
	appID, err := c.findMachineIdentityAppID(ctx, params.OrgID, projectID, loginName)
	if err != nil || appID == "" {
		return false, err
	}
	path := fmt.Sprintf("/management/v1/projects/%s/apps/%s", url.PathEscape(projectID), url.PathEscape(appID))
	if err := c.callInOrg(ctx, params.OrgID, http.MethodDelete, path, nil, nil); err != nil {
		return false, err
	}
	return true, nil
}
