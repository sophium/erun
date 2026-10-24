package eruncommon

import (
	"fmt"
)

type (
	TenantSelectionResult struct {
		Tenant     string
		Initialize bool
	}
)

func CodeCommitSSHKeyIDLabel(tenant, envName string) string {
	return fmt.Sprintf("CodeCommit SSH public key ID for environment %q in tenant %q", envName, tenant)
}
