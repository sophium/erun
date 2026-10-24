package security

import (
	"context"
	"errors"
)

var ErrMissingContext = errors.New("missing security context")

// ErrTenantUnresolved means the token's issuer mapped to no tenant (e.g. missing org claim), unlike "not enrolled".
var ErrTenantUnresolved = errors.New("tenant could not be resolved from token")

type Claims struct {
	Issuer   string
	Subject  string
	Username string
	// Raw keeps all claims because the org claim name is per-issuer and unknown at verify time.
	Raw map[string]any
}

type Context struct {
	TenantID       string
	TenantType     string
	ErunUserID     string
	ExternalIssuer string
	ExternalUserID string
	// ExternalOrgID is the org claim that resolved the tenant; empty for single-tenant issuers.
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
