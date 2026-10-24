package zitadel

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// loginPolicy is the writable field set; decoding a GET into it drops read-only
// fields, making GET-then-write safe.
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
	// IsDefault is absent (false) once the org has an override. It decides POST
	// (first override) vs PUT (existing one); the wrong verb fails.
	IsDefault bool `json:"isDefault"`
}

// MinLength is a string because protobuf JSON encodes uint64 as a string.
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

// OrgSettings is the operator-facing org settings view; domains are read-only.
type OrgSettings struct {
	ForceMFA                  bool     `json:"forceMfa"`
	AllowRegister             bool     `json:"allowRegister"`
	MinPasswordLength         uint64   `json:"minPasswordLength"`
	PasswordRequiresUppercase bool     `json:"passwordRequiresUppercase"`
	PasswordRequiresLowercase bool     `json:"passwordRequiresLowercase"`
	PasswordRequiresNumber    bool     `json:"passwordRequiresNumber"`
	PasswordRequiresSymbol    bool     `json:"passwordRequiresSymbol"`
	VerifiedDomains           []string `json:"verifiedDomains"`
}

// UpdateOrgSettingsParams leaves nil fields unchanged.
type UpdateOrgSettingsParams struct {
	ForceMFA                  *bool
	AllowRegister             *bool
	MinPasswordLength         *uint64
	PasswordRequiresUppercase *bool
	PasswordRequiresLowercase *bool
	PasswordRequiresNumber    *bool
	PasswordRequiresSymbol    *bool
}

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
		AllowRegister:             login.AllowRegister,
		MinPasswordLength:         minLength,
		PasswordRequiresUppercase: complexity.HasUppercase,
		PasswordRequiresLowercase: complexity.HasLowercase,
		PasswordRequiresNumber:    complexity.HasNumber,
		PasswordRequiresSymbol:    complexity.HasSymbol,
		VerifiedDomains:           domains,
	}, nil
}

// UpdateOrgSettings writes only fields that differ: Zitadel answers 400
// "NotChanged" to an unchanged policy.
func (c *Client) UpdateOrgSettings(ctx context.Context, params UpdateOrgSettingsParams) (OrgSettings, error) {
	if hasLoginPolicyChange(params) {
		if err := c.updateLoginPolicy(ctx, params); err != nil {
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

func hasLoginPolicyChange(params UpdateOrgSettingsParams) bool {
	return params.ForceMFA != nil || params.AllowRegister != nil
}

func hasPasswordComplexityChange(params UpdateOrgSettingsParams) bool {
	return params.MinPasswordLength != nil || params.PasswordRequiresUppercase != nil ||
		params.PasswordRequiresLowercase != nil || params.PasswordRequiresNumber != nil ||
		params.PasswordRequiresSymbol != nil
}

func (c *Client) updateLoginPolicy(ctx context.Context, params UpdateOrgSettingsParams) error {
	login, isDefault, err := c.getLoginPolicy(ctx)
	if err != nil {
		return fmt.Errorf("get zitadel login policy: %w", err)
	}
	changed := applyBoolField(&login.ForceMFA, params.ForceMFA)
	changed = applyBoolField(&login.AllowRegister, params.AllowRegister) || changed
	if !changed {
		return nil
	}
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

// applyPasswordComplexityParams applies params in place and reports any real change.
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

// writeLoginPolicy POSTs the first override and PUTs an existing one (see IsDefault).
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
