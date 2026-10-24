package zitadel

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Machine identities are client-credentials applications, one per environment in
// one project per organization, found by a login name derived from the
// environment id so provisioning converges.

// machineIdentityProjectName is kept apart from the platform's shared sign-in clients' project.
const machineIdentityProjectName = "erun-agents"

// JWT is required: the erun-shipped Zitadel asserts the org claim an org-scoped
// issuer resolves the tenant from only on a JWT access token.
const (
	apiAuthMethodBasic = "API_AUTH_METHOD_TYPE_BASIC"
	apiTokenTypeJWT    = "API_TOKEN_TYPE_JWT"
)

const machineIdentityAppDescription = "erun environment machine identity"

// MachineIdentity is one environment's Zitadel application and its credential.
// ClientID is also the token subject, so erun's user row is enrolled under it.
type MachineIdentity struct {
	ClientID     string
	ClientSecret string
}

type EnsureMachineIdentityParams struct {
	// OrgID is the identity's organization; empty means the credential's own.
	OrgID string
	// LoginName is the idempotency key the existing application is looked up by.
	LoginName string
}

// EnsureMachineIdentity finds or creates the identity for params.LoginName,
// correcting a non-JWT access token type on an existing one.
func (c *Client) EnsureMachineIdentity(ctx context.Context, params EnsureMachineIdentityParams) (MachineIdentity, error) {
	loginName := strings.TrimSpace(params.LoginName)
	if loginName == "" {
		return MachineIdentity{}, fmt.Errorf("a login name is required to provision a machine identity")
	}
	projectID, err := c.ensureMachineIdentityProject(ctx, params.OrgID)
	if err != nil {
		return MachineIdentity{}, err
	}
	appID, err := c.findAppID(ctx, params.OrgID, projectID, loginName)
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
	return c.ensureProject(ctx, orgID, machineIdentityProjectName)
}

func (c *Client) ensureProject(ctx context.Context, orgID string, name string) (string, error) {
	projectID, err := c.findProjectID(ctx, orgID, name)
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
		"name": name,
	}, &created); err != nil {
		return "", err
	}
	if strings.TrimSpace(created.ID) == "" {
		return "", fmt.Errorf("zitadel created project %q but returned no id", name)
	}
	return created.ID, nil
}

// findMachineIdentityProjectID finds the project without creating it, for revocation.
func (c *Client) findMachineIdentityProjectID(ctx context.Context, orgID string) (string, error) {
	return c.findProjectID(ctx, orgID, machineIdentityProjectName)
}

func (c *Client) findProjectID(ctx context.Context, orgID string, name string) (string, error) {
	var search struct {
		Result []zitadelProject `json:"result"`
	}
	if err := c.callInOrg(ctx, orgID, http.MethodPost, "/management/v1/projects/_search", map[string]any{}, &search); err != nil {
		return "", err
	}
	for _, project := range search.Result {
		if project.Name == name {
			return project.ID, nil
		}
	}
	return "", nil
}

func (c *Client) findAppID(ctx context.Context, orgID string, projectID string, name string) (string, error) {
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
		if app.Name == name {
			return app.ID, nil
		}
	}
	return "", nil
}

// apiConfig is absent for an OIDC application, so an empty client id is reported
// rather than enrolled as "".
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

// convergeMachineIdentityApp corrects a non-JWT access token type, which would
// leave every token unable to resolve a tenant.
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
		// The update replaces the whole config, so the auth method is carried over.
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

type DeleteMachineIdentityParams struct {
	// OrgID is the identity's organization; empty means the credential's own.
	OrgID     string
	LoginName string
}

// DeleteMachineIdentity removes the identity named by params.LoginName, reporting
// whether one existed. Absence is (false, nil) so the retried environment-delete
// workflow can re-run it; the project is never created.
func (c *Client) DeleteMachineIdentity(ctx context.Context, params DeleteMachineIdentityParams) (bool, error) {
	loginName := strings.TrimSpace(params.LoginName)
	if loginName == "" {
		return false, nil
	}
	projectID, err := c.findMachineIdentityProjectID(ctx, params.OrgID)
	if err != nil || projectID == "" {
		return false, err
	}
	appID, err := c.findAppID(ctx, params.OrgID, projectID, loginName)
	if err != nil || appID == "" {
		return false, err
	}
	path := fmt.Sprintf("/management/v1/projects/%s/apps/%s", url.PathEscape(projectID), url.PathEscape(appID))
	if err := c.callInOrg(ctx, params.OrgID, http.MethodDelete, path, nil, nil); err != nil {
		return false, err
	}
	return true, nil
}
