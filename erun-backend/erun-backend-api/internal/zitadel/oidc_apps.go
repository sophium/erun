package zitadel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// oidc_apps.go is the Management API binding for the platform's own OIDC
// sign-in applications (erun-console, erun-cli, erun-mobile), which the
// erun-zitadel chart's oidc-bootstrap sidecar reconciles through
// internal/oidcbootstrap. Every call acts in the credential's own
// organization: those applications are the platform's, shared by every
// tenant, so there is no other org to address.

// EnsureProject returns the id of the project named name, creating it when
// the credential's organization holds none.
func (c *Client) EnsureProject(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("a project name is required")
	}
	return c.ensureProject(ctx, "", name)
}

// FindAppID returns the id of the application named name in projectID, or ""
// when the project holds none.
func (c *Client) FindAppID(ctx context.Context, projectID string, name string) (string, error) {
	return c.findAppID(ctx, "", projectID, name)
}

// OIDCApp is an existing OIDC application as the Management API reports it.
//
// Config is the live oidcConfig kept field by field rather than decoded into
// a struct, because the config update endpoint replaces the whole config: a
// caller changing one field has to send every other one back exactly as it
// was, including fields this package never names.
type OIDCApp struct {
	ClientID string
	Config   map[string]json.RawMessage
}

// StringList decodes the string-list field key of the live config, treating
// an absent or null field as empty.
func (a OIDCApp) StringList(key string) ([]string, error) {
	raw, ok := a.Config[key]
	if !ok || string(raw) == "null" {
		return []string{}, nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("decode oidcConfig.%s: %w", key, err)
	}
	if values == nil {
		values = []string{}
	}
	return values, nil
}

// GetOIDCApp reads one OIDC application of projectID.
func (c *Client) GetOIDCApp(ctx context.Context, projectID string, appID string) (OIDCApp, error) {
	var response struct {
		App struct {
			OIDCConfig map[string]json.RawMessage `json:"oidcConfig"`
		} `json:"app"`
	}
	path := fmt.Sprintf("/management/v1/projects/%s/apps/%s", url.PathEscape(projectID), url.PathEscape(appID))
	if err := c.call(ctx, http.MethodGet, path, nil, &response); err != nil {
		return OIDCApp{}, err
	}
	config := response.App.OIDCConfig
	if config == nil {
		return OIDCApp{}, fmt.Errorf("zitadel application %s is not an oidc application", appID)
	}
	app := OIDCApp{Config: config}
	if raw, ok := config["clientId"]; ok {
		if err := json.Unmarshal(raw, &app.ClientID); err != nil {
			return OIDCApp{}, fmt.Errorf("decode oidcConfig.clientId of application %s: %w", appID, err)
		}
	}
	if strings.TrimSpace(app.ClientID) == "" {
		return OIDCApp{}, fmt.Errorf("zitadel application %s reports no client id", appID)
	}
	return app, nil
}

// OIDCAppSpec is the creation input for an OIDC application.
type OIDCAppSpec struct {
	Name                   string   `json:"name"`
	RedirectURIs           []string `json:"redirectUris"`
	ResponseTypes          []string `json:"responseTypes"`
	GrantTypes             []string `json:"grantTypes"`
	AppType                string   `json:"appType"`
	AuthMethodType         string   `json:"authMethodType"`
	PostLogoutRedirectURIs []string `json:"postLogoutRedirectUris,omitempty"`
	AccessTokenType        string   `json:"accessTokenType"`
}

// CreateOIDCApp creates an OIDC application in projectID and returns its
// client id.
func (c *Client) CreateOIDCApp(ctx context.Context, projectID string, spec OIDCAppSpec) (string, error) {
	var created struct {
		ClientID string `json:"clientId"`
	}
	path := fmt.Sprintf("/management/v1/projects/%s/apps/oidc", url.PathEscape(projectID))
	if err := c.call(ctx, http.MethodPost, path, spec, &created); err != nil {
		return "", err
	}
	if strings.TrimSpace(created.ClientID) == "" {
		return "", fmt.Errorf("zitadel created oidc application %q but returned no client id", spec.Name)
	}
	return created.ClientID, nil
}

// UpdateOIDCConfig replaces the OIDC config of an application. The endpoint
// replaces rather than merges, so config must carry every field the caller
// wants kept (see OIDCApp).
func (c *Client) UpdateOIDCConfig(ctx context.Context, projectID string, appID string, config map[string]json.RawMessage) error {
	path := fmt.Sprintf("/management/v1/projects/%s/apps/%s/oidc_config", url.PathEscape(projectID), url.PathEscape(appID))
	return c.call(ctx, http.MethodPut, path, config, nil)
}
