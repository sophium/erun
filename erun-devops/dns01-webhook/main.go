package main

import (
	"os"

	"github.com/cert-manager/cert-manager/pkg/acme/webhook/cmd"
)

// groupName is the API group the webhook registers under (GROUP_NAME env), which
// the per-tenant Issuer's webhook solver references. Set by the chart.
func main() {
	groupName := os.Getenv("GROUP_NAME")
	if groupName == "" {
		panic("GROUP_NAME must be set")
	}
	cmd.RunWebhookServer(groupName, &brokerSolver{})
}
