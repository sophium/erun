package main

import (
	"errors"
	"fmt"
	"strings"
)

// ErrTenantNotGiven is the desktop's own tenant-input validation failure: a
// Wails-exported handler that operates on a tenant received an empty one.
// Every caller of requireTenant receives tenant from state the frontend
// already resolved before the call fired — the dashboard's own tenant, a
// dialog's tenant prop — so hitting this means that state was not resolved
// yet, not that the operator typed nothing; there is no field for them to
// fill in, only a surface to reopen.
var ErrTenantNotGiven = errors.New("no tenant given")

// requireTenant trims tenant and, if empty, reports which desktop operation
// needed one and the one recovery actually available: reopening the tab or
// dialog re-resolves the tenant from app state.
func requireTenant(operation, tenant string) (string, error) {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return "", fmt.Errorf("%w: %s needs a tenant, and none was given — close and reopen the tab or dialog and try again", ErrTenantNotGiven, operation)
	}
	return tenant, nil
}
