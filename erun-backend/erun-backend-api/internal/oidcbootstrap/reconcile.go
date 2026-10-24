// Package oidcbootstrap reconciles the platform's OIDC applications in Zitadel
// and persists core's bootstrap PATs to a Secret, as the erun-zitadel sidecar.
//
// It is a sidecar, not a hook Job, because it needs core's bootstrap emptyDir.
// Redirect URIs converge every tick, but never to an empty list, and the
// client-id ConfigMap is published only when every app resolved.
package oidcbootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"

	"k8s.io/client-go/kubernetes"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/zitadel"
)

const (
	AdminPATFile = "admin-sa.pat"
	LoginPATFile = "login-client.pat"
)

const (
	ConsoleAppName = "erun-console"
	CLIAppName     = "erun-cli"
	MobileAppName  = "erun-mobile"

	ConsoleClientIDKey = "consoleClientId"
	CLIClientIDKey     = "cliClientId"
	MobileClientIDKey  = "mobileClientId"
)

// CLIRedirectURIs are erun-cli's loopback redirects.
var CLIRedirectURIs = []string{"http://127.0.0.1/callback", "http://localhost/callback"}

// erun's bearer verifier rejects Zitadel's default opaque tokens, so every app issues JWTs.
const (
	responseTypeCode    = "OIDC_RESPONSE_TYPE_CODE"
	grantTypeAuthCode   = "OIDC_GRANT_TYPE_AUTHORIZATION_CODE"
	grantTypeDeviceCode = "OIDC_GRANT_TYPE_DEVICE_CODE"
	appTypeUserAgent    = "OIDC_APP_TYPE_USER_AGENT"
	appTypeNative       = "OIDC_APP_TYPE_NATIVE"
	authMethodNone      = "OIDC_AUTH_METHOD_TYPE_NONE"
	accessTokenTypeJWT  = "OIDC_TOKEN_TYPE_JWT"
	redirectURIsField   = "redirectUris"
	postLogoutURIsField = "postLogoutRedirectUris"
)

// Config names what one reconcile tick acts on.
type Config struct {
	ProjectName    string
	ConfigMapName  string
	PATsSecretName string
	Namespace      string
	BootstrapDir   string
	// Empty redirect lists leave that app uncreated.
	ConsoleRedirectURIs []string
	MobileRedirectURIs  []string
}

// Reconciler performs reconcile ticks.
type Reconciler struct {
	cfg  Config
	idp  *zitadel.Client
	kube kubernetes.Interface
	log  *log.Logger
}

// NewReconciler returns a Reconciler driving idp and kube.
func NewReconciler(cfg Config, idp *zitadel.Client, kube kubernetes.Interface, logger *log.Logger) *Reconciler {
	if logger == nil {
		logger = log.New(os.Stderr, "oidc-bootstrap: ", log.LstdFlags|log.Lmsgprefix)
	}
	return &Reconciler{cfg: cfg, idp: idp, kube: kube, log: logger}
}

type appDefinition struct {
	spec           zitadel.OIDCAppSpec
	optional       bool
	convergeLogout bool
}

func (r *Reconciler) apps() []appDefinition {
	return []appDefinition{
		{
			spec: zitadel.OIDCAppSpec{
				Name:                   ConsoleAppName,
				RedirectURIs:           r.cfg.ConsoleRedirectURIs,
				ResponseTypes:          []string{responseTypeCode},
				GrantTypes:             []string{grantTypeAuthCode},
				AppType:                appTypeUserAgent,
				AuthMethodType:         authMethodNone,
				PostLogoutRedirectURIs: r.cfg.ConsoleRedirectURIs,
				AccessTokenType:        accessTokenTypeJWT,
			},
			optional:       true,
			convergeLogout: true,
		},
		{
			spec: zitadel.OIDCAppSpec{
				Name:            CLIAppName,
				RedirectURIs:    CLIRedirectURIs,
				ResponseTypes:   []string{responseTypeCode},
				GrantTypes:      []string{grantTypeAuthCode, grantTypeDeviceCode},
				AppType:         appTypeNative,
				AuthMethodType:  authMethodNone,
				AccessTokenType: accessTokenTypeJWT,
			},
		},
		{
			// A mobile client always has a system browser, so PKCE only.
			spec: zitadel.OIDCAppSpec{
				Name:            MobileAppName,
				RedirectURIs:    r.cfg.MobileRedirectURIs,
				ResponseTypes:   []string{responseTypeCode},
				GrantTypes:      []string{grantTypeAuthCode},
				AppType:         appTypeNative,
				AuthMethodType:  authMethodNone,
				AccessTokenType: accessTokenTypeJWT,
			},
			optional: true,
		},
	}
}

// Reconcile performs one tick. Client ids are published even when a
// resolved app's convergence fails, and that failure is still returned.
func (r *Reconciler) Reconcile(ctx context.Context) error {
	if err := r.persistPATs(ctx); err != nil {
		r.log.Printf("PATs Secret not persisted this tick: %v", err)
	}

	projectID, err := r.idp.EnsureProject(ctx, r.cfg.ProjectName)
	if err != nil {
		r.log.Printf("resolution incomplete (project %q: %v), leaving %s untouched", r.cfg.ProjectName, err, r.cfg.ConfigMapName)
		return fmt.Errorf("resolve project %q: %w", r.cfg.ProjectName, err)
	}

	clientIDs := map[string]string{}
	var convergeErrs []error
	for _, app := range r.apps() {
		clientID, convergeErr, err := r.ensureApp(ctx, projectID, app)
		if err != nil {
			r.log.Printf("resolution incomplete (project=%s %s: %v), leaving %s untouched", projectID, app.spec.Name, err, r.cfg.ConfigMapName)
			return fmt.Errorf("resolve %s: %w", app.spec.Name, err)
		}
		if convergeErr != nil {
			convergeErrs = append(convergeErrs, convergeErr)
		}
		clientIDs[app.spec.Name] = clientID
	}

	if err := applyConfigMap(ctx, r.kube, r.cfg.Namespace, r.cfg.ConfigMapName, map[string]string{
		ConsoleClientIDKey: clientIDs[ConsoleAppName],
		CLIClientIDKey:     clientIDs[CLIAppName],
		MobileClientIDKey:  clientIDs[MobileAppName],
	}); err != nil {
		r.log.Printf("FAILED to publish %s: %v", r.cfg.ConfigMapName, err)
		return fmt.Errorf("publish %s: %w", r.cfg.ConfigMapName, err)
	}
	r.log.Printf("ready project=%s console=%s cli=%s mobile=%s", projectID, clientIDs[ConsoleAppName], clientIDs[CLIAppName], clientIDs[MobileAppName])
	return errors.Join(convergeErrs...)
}

func (r *Reconciler) ensureApp(ctx context.Context, projectID string, app appDefinition) (clientID string, convergeErr error, err error) {
	name := app.spec.Name
	appID, err := r.idp.FindAppID(ctx, projectID, name)
	if err != nil {
		return "", nil, err
	}
	if appID == "" {
		if app.optional && len(app.spec.RedirectURIs) == 0 {
			r.log.Printf("no %s redirect URI configured; skipping the %s app", shortName(name), name)
			return "", nil, nil
		}
		clientID, err := r.idp.CreateOIDCApp(ctx, projectID, app.spec)
		if err != nil {
			return "", nil, err
		}
		r.log.Printf("created %s with redirect URIs %s", name, jsonList(app.spec.RedirectURIs))
		return clientID, nil, nil
	}
	live, err := r.idp.GetOIDCApp(ctx, projectID, appID)
	if err != nil {
		r.log.Printf("FAILED to read the %s app; leaving it unchanged: %v", name, err)
		return "", nil, err
	}
	return live.ClientID, r.converge(ctx, projectID, appID, app, live), nil
}

func shortName(name string) string {
	switch name {
	case ConsoleAppName:
		return "console"
	case MobileAppName:
		return "mobile"
	default:
		return name
	}
}

// The update endpoint replaces the whole config, so these are carried over.
var updatableOIDCFields = []string{
	"responseTypes", "grantTypes", "appType", "authMethodType",
	redirectURIsField, postLogoutURIsField,
	"accessTokenType", "devMode", "accessTokenRoleAssertion", "idTokenRoleAssertion",
	"idTokenUserinfoAssertion", "clockSkew", "additionalOrigins", "skipNativeAppSuccessPage",
}

func (r *Reconciler) converge(ctx context.Context, projectID string, appID string, app appDefinition, live zitadel.OIDCApp) error {
	name := app.spec.Name
	desired := app.spec.RedirectURIs
	current, inSync, err := registeredState(live, app)
	if err != nil {
		r.log.Printf("FAILED to converge %s redirect URIs: %v", name, err)
		return fmt.Errorf("converge %s: %w", name, err)
	}
	if inSync {
		return nil
	}
	if len(desired) == 0 {
		r.log.Printf("%s has no configured redirect URI; leaving its registered %s untouched", name, sortedJSON(current))
		return nil
	}
	if err := r.idp.UpdateOIDCConfig(ctx, projectID, appID, convergedConfig(live, app)); err != nil {
		r.log.Printf("FAILED to converge %s redirect URIs (wanted %s); app left unchanged: %v", name, sortedJSON(desired), err)
		return fmt.Errorf("converge %s redirect URIs: %w", name, err)
	}
	r.log.Printf("converged %s redirect URIs from %s to %s", name, sortedJSON(current), sortedJSON(desired))
	return nil
}

func registeredState(live zitadel.OIDCApp, app appDefinition) (current []string, inSync bool, err error) {
	current, err = live.StringList(redirectURIsField)
	if err != nil {
		return nil, false, err
	}
	inSync = sameSet(current, app.spec.RedirectURIs)
	if app.convergeLogout {
		currentLogout, err := live.StringList(postLogoutURIsField)
		if err != nil {
			return nil, false, err
		}
		inSync = inSync && sameSet(currentLogout, app.spec.RedirectURIs)
	}
	return current, inSync, nil
}

func convergedConfig(live zitadel.OIDCApp, app appDefinition) map[string]json.RawMessage {
	update := make(map[string]json.RawMessage, len(updatableOIDCFields))
	for _, field := range updatableOIDCFields {
		if raw, ok := live.Config[field]; ok {
			update[field] = raw
		}
	}
	update[redirectURIsField] = rawList(app.spec.RedirectURIs)
	if app.convergeLogout {
		update[postLogoutURIsField] = rawList(app.spec.RedirectURIs)
	}
	return update
}

func (r *Reconciler) persistPATs(ctx context.Context) error {
	data := map[string][]byte{}
	for _, file := range []string{AdminPATFile, LoginPATFile} {
		content, err := os.ReadFile(filepath.Join(r.cfg.BootstrapDir, file))
		if err != nil || len(content) == 0 {
			return fmt.Errorf("%s not yet written, skipping persist", file)
		}
		data[file] = content
	}
	return applySecret(ctx, r.kube, r.cfg.Namespace, r.cfg.PATsSecretName, data)
}

func sameSet(a []string, b []string) bool {
	return slices.Equal(sorted(a), sorted(b))
}

func sorted(values []string) []string {
	out := append([]string{}, values...)
	slices.Sort(out)
	return out
}

func jsonList(values []string) string {
	return string(rawList(values))
}

func sortedJSON(values []string) string {
	return jsonList(sorted(values))
}

func rawList(values []string) json.RawMessage {
	if values == nil {
		values = []string{}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return json.RawMessage(`[]`)
	}
	return encoded
}
