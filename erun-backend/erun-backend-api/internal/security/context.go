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
}

type Context struct {
	TenantID       string
	TenantType     string
	ErunUserID     string
	ExternalIssuer string
	ExternalUserID string
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
