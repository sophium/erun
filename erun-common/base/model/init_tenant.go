package model

import (
	"fmt"
	"strings"
)

// TenantSelectionUnavailableError names the flag that resolves the choice, since
// nobody can answer a prompt.
type TenantSelectionUnavailableError struct {
	Tenants []string
}

func (e TenantSelectionUnavailableError) Error() string {
	return fmt.Sprintf("a tenant has to be chosen and this run has nobody to ask; pass --tenant with one of: %s", strings.Join(e.Tenants, ", "))
}

type (
	TenantSelectionResult struct {
		Tenant     string
		Initialize bool
	}
)
