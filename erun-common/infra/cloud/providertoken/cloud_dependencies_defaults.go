package providertoken

import (
	"strings"
)

func isAWSOutboundWebIdentityFederationDisabled(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "outboundwebidentityfederationdisabledexception") ||
		strings.Contains(message, "outboundwebidentityfederation is disabled") ||
		strings.Contains(message, "outbound web identity federation is disabled")
}
