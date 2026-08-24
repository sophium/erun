package zitadel

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// loginPolicy is the writable field set Zitadel's org login policy accepts,
// proven against a real instance by
// erun-console/playwright/zitadel/provision.sh (which pins these exact
// fields to make sign-in deterministic). GET responses wrap this under
// "policy" alongside read-only fields (isDefault, details, ...); decoding
// into this struct drops them, which is what makes a GET-then-POST
// round-trip safe as an update body.
type loginPolicy struct {
	AllowUsernamePassword      bool   `json:"allowUsernamePassword"`
	AllowRegister              bool   `json:"allowRegister"`
	AllowExternalIdp           bool   `json:"allowExternalIdp"`
	ForceMFA                   bool   `json:"forceMfa"`
	ForceMFALocalOnly          bool   `json:"forceMfaLocalOnly"`
	PasswordlessType           string `json:"passwordlessType"`
	HidePasswordReset          bool   `json:"hidePasswordReset"`
	AllowDomainDiscovery       bool   `json:"allowDomainDiscovery"`
	PasswordCheckLifetime      string `json:"passwordCheckLifetime"`
	ExternalLoginCheckLifetime string `json:"externalLoginCheckLifetime"`
	MFAInitSkipLifetime        string `json:"mfaInitSkipLifetime"`
	SecondFactorCheckLifetime  string `json:"secondFactorCheckLifetime"`
	MultiFactorCheckLifetime   string `json:"multiFactorCheckLifetime"`
}

type loginPolicyResponse struct {
	Policy loginPolicy `json:"policy"`
	// IsDefault reports whether the org has never overridden the instance
	// default. Proto3 omits false fields, so IsDefault is absent (== false)
	// once an org-level override exists — verified live: writing an override
	// makes both this field and policy.isDefault disappear from later GETs.
	// This is what decides POST (create the org's first override) vs PUT
	// (update its existing one) in UpdateOrgSettings — POST against an
	// already-overridden org fails with "Login Policy already exists", and
	// PUT against a still-default org fails with "not found".
	IsDefault bool `json:"isDefault"`
}

// passwordComplexityPolicy is the writable field set for the org password
// complexity policy. Zitadel's gRPC-gateway serializes the proto uint64
// MinLength as a JSON string (protobuf JSON mapping for 64-bit integers), so
// the wire type here is string; OrgSettings exposes it as a real number.
type passwordComplexityPolicy struct {
	MinLength    string `json:"minLength"`
	HasUppercase bool   `json:"hasUppercase"`
	HasLowercase bool   `json:"hasLowercase"`
	HasNumber    bool   `json:"hasNumber"`
	HasSymbol    bool   `json:"hasSymbol"`
}

type passwordComplexityPolicyResponse struct {
	Policy    passwordComplexityPolicy `json:"policy"`
	IsDefault bool                     `json:"isDefault"`
}

type domainSearchResponse struct {
	Result []struct {
		DomainName string `json:"domainName"`
		IsVerified bool   `json:"isVerified"`
	} `json:"result"`
}

// OrgSettings is the operator-facing view of the org settings an operator
// actually changes: whether MFA is required, the password complexity rules,
// and the org's verified domains (read-only here — verifying a domain is a
// DNS/HTTP challenge flow this surface does not drive).
type OrgSettings struct {
	ForceMFA                  bool     `json:"forceMfa"`
	MinPasswordLength         uint64   `json:"minPasswordLength"`
	PasswordRequiresUppercase bool     `json:"passwordRequiresUppercase"`
	PasswordRequiresLowercase bool     `json:"passwordRequiresLowercase"`
	PasswordRequiresNumber    bool     `json:"passwordRequiresNumber"`
	PasswordRequiresSymbol    bool     `json:"passwordRequiresSymbol"`
	VerifiedDomains           []string `json:"verifiedDomains"`
}

// UpdateOrgSettingsParams carries only the fields the caller wants to
// change; nil fields are left at their current Zitadel value by the
// GET-then-POST round-trip in UpdateOrgSettings.
type UpdateOrgSettingsParams struct {
	ForceMFA                  *bool
	MinPasswordLength         *uint64
	PasswordRequiresUppercase *bool
	PasswordRequiresLowercase *bool
	PasswordRequiresNumber    *bool
	PasswordRequiresSymbol    *bool
}

// GetOrgSettings reads the org's current login policy, password complexity
// policy, and verified domains.
func (c *Client) GetOrgSettings(ctx context.Context) (OrgSettings, error) {
	login, _, err := c.getLoginPolicy(ctx)
	if err != nil {
		return OrgSettings{}, fmt.Errorf("get zitadel login policy: %w", err)
	}
	complexity, _, err := c.getPasswordComplexityPolicy(ctx)
	if err != nil {
		return OrgSettings{}, fmt.Errorf("get zitadel password complexity policy: %w", err)
	}
	minLength, err := parseMinLength(complexity.MinLength)
	if err != nil {
		return OrgSettings{}, fmt.Errorf("parse zitadel password complexity minLength %q: %w", complexity.MinLength, err)
	}
	domains, err := c.listVerifiedDomains(ctx)
	if err != nil {
		return OrgSettings{}, fmt.Errorf("list zitadel org domains: %w", err)
	}
	return OrgSettings{
		ForceMFA:                  login.ForceMFA,
		MinPasswordLength:         minLength,
		PasswordRequiresUppercase: complexity.HasUppercase,
		PasswordRequiresLowercase: complexity.HasLowercase,
		PasswordRequiresNumber:    complexity.HasNumber,
		PasswordRequiresSymbol:    complexity.HasSymbol,
		VerifiedDomains:           domains,
	}, nil
}

// UpdateOrgSettings applies only the fields set in params that actually
// differ from the org's current value, preserving every other field via a
// read-modify-write. Writing only on a genuine diff (the same convergence
// discipline erun-zitadel's oidc-bootstrap sidecar uses for OIDC redirect
// URIs) sidesteps a real Zitadel behavior: re-sending an unchanged policy
// answers 400 "NotChanged" rather than a no-op 200.
func (c *Client) UpdateOrgSettings(ctx context.Context, params UpdateOrgSettingsParams) (OrgSettings, error) {
	if params.ForceMFA != nil {
		if err := c.updateLoginPolicy(ctx, *params.ForceMFA); err != nil {
			return OrgSettings{}, err
		}
	}
	if hasPasswordComplexityChange(params) {
		if err := c.updatePasswordComplexityPolicy(ctx, params); err != nil {
			return OrgSettings{}, err
		}
	}
	return c.GetOrgSettings(ctx)
}

func hasPasswordComplexityChange(params UpdateOrgSettingsParams) bool {
	return params.MinPasswordLength != nil || params.PasswordRequiresUppercase != nil ||
		params.PasswordRequiresLowercase != nil || params.PasswordRequiresNumber != nil ||
		params.PasswordRequiresSymbol != nil
}

func (c *Client) updateLoginPolicy(ctx context.Context, forceMFA bool) error {
	login, isDefault, err := c.getLoginPolicy(ctx)
	if err != nil {
		return fmt.Errorf("get zitadel login policy: %w", err)
	}
	if login.ForceMFA == forceMFA {
		return nil
	}
	login.ForceMFA = forceMFA
	if err := c.writeLoginPolicy(ctx, login, isDefault); err != nil {
		return fmt.Errorf("update zitadel login policy: %w", err)
	}
	return nil
}

func (c *Client) updatePasswordComplexityPolicy(ctx context.Context, params UpdateOrgSettingsParams) error {
	complexity, isDefault, err := c.getPasswordComplexityPolicy(ctx)
	if err != nil {
		return fmt.Errorf("get zitadel password complexity policy: %w", err)
	}
	if !applyPasswordComplexityParams(&complexity, params) {
		return nil
	}
	if err := c.writePasswordComplexityPolicy(ctx, complexity, isDefault); err != nil {
		return fmt.Errorf("update zitadel password complexity policy: %w", err)
	}
	return nil
}

// applyPasswordComplexityParams mutates complexity in place with every field
// params sets and reports whether any of them was a genuine change from the
// value already there.
func applyPasswordComplexityParams(complexity *passwordComplexityPolicy, params UpdateOrgSettingsParams) bool {
	changed := false
	if params.MinPasswordLength != nil {
		changed = applyStringField(&complexity.MinLength, strconv.FormatUint(*params.MinPasswordLength, 10)) || changed
	}
	changed = applyBoolField(&complexity.HasUppercase, params.PasswordRequiresUppercase) || changed
	changed = applyBoolField(&complexity.HasLowercase, params.PasswordRequiresLowercase) || changed
	changed = applyBoolField(&complexity.HasNumber, params.PasswordRequiresNumber) || changed
	changed = applyBoolField(&complexity.HasSymbol, params.PasswordRequiresSymbol) || changed
	return changed
}

// applyBoolField sets *field to *value and reports true when that is a
// genuine change; a nil value (the caller didn't ask to change this field)
// or a value matching the current one is a no-op that reports false.
func applyBoolField(field *bool, value *bool) bool {
	if value == nil || *field == *value {
		return false
	}
	*field = *value
	return true
}

func applyStringField(field *string, value string) bool {
	if *field == value {
		return false
	}
	*field = value
	return true
}

func (c *Client) getLoginPolicy(ctx context.Context) (loginPolicy, bool, error) {
	var resp loginPolicyResponse
	if err := c.call(ctx, http.MethodGet, "/management/v1/policies/login", nil, &resp); err != nil {
		return loginPolicy{}, false, err
	}
	return resp.Policy, resp.IsDefault, nil
}

// writeLoginPolicy creates the org's first override with POST when it is
// still on the instance default, or updates its existing override with PUT
// otherwise — the two verbs are not interchangeable (see IsDefault's doc).
func (c *Client) writeLoginPolicy(ctx context.Context, policy loginPolicy, isDefault bool) error {
	method := http.MethodPut
	if isDefault {
		method = http.MethodPost
	}
	return c.call(ctx, method, "/management/v1/policies/login", policy, nil)
}

func (c *Client) getPasswordComplexityPolicy(ctx context.Context) (passwordComplexityPolicy, bool, error) {
	var resp passwordComplexityPolicyResponse
	if err := c.call(ctx, http.MethodGet, "/management/v1/policies/password/complexity", nil, &resp); err != nil {
		return passwordComplexityPolicy{}, false, err
	}
	return resp.Policy, resp.IsDefault, nil
}

func (c *Client) writePasswordComplexityPolicy(ctx context.Context, policy passwordComplexityPolicy, isDefault bool) error {
	method := http.MethodPut
	if isDefault {
		method = http.MethodPost
	}
	return c.call(ctx, method, "/management/v1/policies/password/complexity", policy, nil)
}

func (c *Client) listVerifiedDomains(ctx context.Context) ([]string, error) {
	var resp domainSearchResponse
	if err := c.call(ctx, http.MethodPost, "/management/v1/orgs/me/domains/_search", map[string]any{}, &resp); err != nil {
		return nil, err
	}
	domains := make([]string, 0, len(resp.Result))
	for _, d := range resp.Result {
		if d.IsVerified {
			domains = append(domains, d.DomainName)
		}
	}
	return domains, nil
}

func parseMinLength(value string) (uint64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	return strconv.ParseUint(value, 10, 64)
}
