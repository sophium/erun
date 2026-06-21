package security

import (
	"context"
	"errors"
)

var ErrMissingContext = errors.New("missing security context")

type Claims struct {
	Issuer   string
	Subject  string
	Username string
	// Raw is the full set of token claims, kept so the identity resolver can
	// read a per-issuer org claim (issuers.org_field_key) for (iss, org) -> tenant
	// resolution. The claim's name is configured per issuer, not known at verify time.
	Raw map[string]any
}

type Context struct {
	TenantID       string
	TenantType     string
	ErunUserID     string
	ExternalIssuer string
	ExternalUserID string
	// ExternalOrgID is the org/resource-owner claim value that, together with
	// ExternalIssuer, resolved the tenant for an org-scoped (shared) issuer.
	// Empty for single-tenant issuers, where the issuer alone resolves the tenant.
	ExternalOrgID string
}

type contextKey struct{}

func WithContext(ctx context.Context, securityContext Context) context.Context {
	return context.WithValue(ctx, contextKey{}, securityContext)
}

func FromContext(ctx context.Context) (Context, bool) {
	securityContext, ok := ctx.Value(contextKey{}).(Context)
	return securityContext, ok
}

func RequiredFromContext(ctx context.Context) (Context, error) {
	securityContext, ok := FromContext(ctx)
	if !ok {
		return Context{}, ErrMissingContext
	}
	return securityContext, nil
}
