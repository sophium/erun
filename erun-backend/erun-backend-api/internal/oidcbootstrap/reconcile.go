// Package oidcbootstrap reconciles the platform's OIDC sign-in applications
// (erun-console, erun-cli, erun-mobile) against Zitadel's Management API and
// persists the two bootstrap PATs Zitadel core mints into a durable Secret. It
// is what `eapi oidc-bootstrap` runs as the erun-zitadel chart's sidecar.
//
// It runs as a sidecar, not a hook Job, because it needs the org-owner PAT
// core writes to the pod's shared bootstrap emptyDir, which a separate Job
// cannot mount. Every Management API call carries the external domain as its
// Host (internal/zitadel does that): Zitadel resolves the instance a call
// targets from that header, not from the loopback address the call is sent to.
//
// Reconciliation, not create-once: each app's registered redirect URIs are
// converged to the configured list on every tick through the OIDC config
// update endpoint. An app that already matches is left alone — no write, no
// log line. A change is logged from what to what; a failed update is logged
// loudly rather than silently leaving a stale registration in place, which is
// what let a changed console address go unnoticed until sign-in broke.
// Converging to an empty list is refused: an operator who unsets every
// configured redirect keeps whatever is registered rather than losing sign-in
// outright, mirroring the ConfigMap publish guard below.
//
// The client-id ConfigMap is never published unless every app genuinely
// resolved. The console and mobile ids are empty on purpose when no redirect
// URI is configured for them (a platform with no mobile client yet leaves
// erun-mobile unconfigured — its custom URL scheme belongs to whichever client
// ships), but an app that could not be resolved aborts the publish, so a
// failure leaves prior good data alone instead of overwriting it with empty
// strings.
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

// The two PAT files Zitadel core writes into the bootstrap directory at
// first-instance init, and the Secret keys they are persisted under.
const (
	AdminPATFile = "admin-sa.pat"
	LoginPATFile = "login-client.pat"
)

// The OIDC applications this package reconciles, and the ConfigMap keys their
// client ids are published under.
const (
	ConsoleAppName = "erun-console"
	CLIAppName     = "erun-cli"
	MobileAppName  = "erun-mobile"

	ConsoleClientIDKey = "consoleClientId"
	CLIClientIDKey     = "cliClientId"
	MobileClientIDKey  = "mobileClientId"
)

// CLIRedirectURIs are erun-cli's loopback redirects. Nothing configures them
// today, but the app is reconciled the same way as erun-console so the two do
// not diverge in shape.
var CLIRedirectURIs = []string{"http://127.0.0.1/callback", "http://localhost/callback"}

// Zitadel's enum values for the application properties set at creation. Every
// app issues JWT access tokens: erun's bearer verifier validates a JWT through
// discovery and JWKS and rejects Zitadel's default opaque token.
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
	// ProjectName is the Zitadel project the three applications live in.
	ProjectName string
	// ConfigMapName is the ConfigMap the client ids are published to.
	ConfigMapName string
	// PATsSecretName is the Secret both bootstrap PATs are persisted to.
	PATsSecretName string
	// Namespace holds the ConfigMap and the Secret.
	Namespace string
	// BootstrapDir is the shared emptyDir core writes the PATs into.
	BootstrapDir string
	// ConsoleRedirectURIs and MobileRedirectURIs are the configured redirect
	// lists; empty leaves that app uncreated.
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

// appDefinition is one OIDC application's desired state.
type appDefinition struct {
	spec zitadel.OIDCAppSpec
	// optional apps are not created while no redirect URI is configured.
	optional bool
	// convergeLogout also converges postLogoutRedirectUris to the redirects.
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
			// A mobile client always has a system browser to redirect
			// through, so erun-mobile is Authorization Code + PKCE only.
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

// Reconcile performs one tick: persist the PATs, resolve the project and every
// app, then publish the client ids. A returned error means the tick was
// incomplete; a convergence failure is still reported after the client ids
// were published, since the app itself resolved.
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

// ensureApp resolves one app to its client id, creating it when absent and
// converging its redirect URIs when present. err means the app did not
// resolve; convergeErr means it resolved but its registration could not be
// brought up to date.
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

// shortName is the word the skip log line names an app by.
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

// updatableOIDCFields are the oidcConfig fields the update endpoint accepts.
// It replaces the whole config rather than merging, so each one present on the
// live app is carried over unchanged unless the reconcile means to change it.
var updatableOIDCFields = []string{
	"responseTypes", "grantTypes", "appType", "authMethodType",
	redirectURIsField, postLogoutURIsField,
	"accessTokenType", "devMode", "accessTokenRoleAssertion", "idTokenRoleAssertion",
	"idTokenUserinfoAssertion", "clockSkew", "additionalOrigins", "skipNativeAppSuccessPage",
}

// converge brings the app's registered redirect URIs (and, for an app that
// converges them, post-logout redirect URIs) to the configured list, writing
// only on a difference.
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

// registeredState reads the app's registered redirect URIs and reports whether
// they (and, for an app that converges them, its post-logout redirect URIs)
// already match the configured list, order-insensitively.
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

// convergedConfig is the live config with the configured redirect URIs in
// place, carrying every other updatable field over unchanged.
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

// persistPATs copies both bootstrap PATs into the durable Secret the
// restore-pats init container seeds a restarted pod from.
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
		// A []string always encodes; this only keeps the result a valid list.
		return json.RawMessage(`[]`)
	}
	return encoded
}
