package main

import (
	"os"

	"github.com/cert-manager/cert-manager/pkg/acme/webhook/cmd"
)

// GROUP_NAME must match the per-tenant Issuer's webhook solver groupName.
func main() {
	groupName := os.Getenv("GROUP_NAME")
	if groupName == "" {
		panic("GROUP_NAME must be set")
	}
	cmd.RunWebhookServer(groupName, &brokerSolver{})
}
