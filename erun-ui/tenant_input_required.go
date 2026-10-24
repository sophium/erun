package main

import (
	"errors"
	"fmt"
	"strings"
)

// ErrTenantNotGiven means frontend state was not resolved yet, not that the operator typed nothing.
var ErrTenantNotGiven = errors.New("no tenant given")

// requireTenant's error tells the operator to reopen the surface, which re-resolves the tenant.
func requireTenant(operation, tenant string) (string, error) {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return "", fmt.Errorf("%w: %s needs a tenant, and none was given — close and reopen the tab or dialog and try again", ErrTenantNotGiven, operation)
	}
	return tenant, nil
}
