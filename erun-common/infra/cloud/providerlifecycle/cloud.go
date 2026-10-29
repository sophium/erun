package providerlifecycle

import (
	"fmt"
	"strings"
)

// Auto prefers the device grant and falls back to auth code + PKCE, so one unusable
// method never locks the CLI out.
const (
	ERunLoginFlowAuto     = "auto"
	ERunLoginFlowDevice   = "device"
	ERunLoginFlowAuthCode = "authcode"
)

// NormalizeERunLoginFlow validates a flow and resolves empty to auto.
func NormalizeERunLoginFlow(flow string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(flow)) {
	case "", ERunLoginFlowAuto:
		return ERunLoginFlowAuto, nil
	case ERunLoginFlowDevice:
		return ERunLoginFlowDevice, nil
	case ERunLoginFlowAuthCode:
		return ERunLoginFlowAuthCode, nil
	default:
		return "", fmt.Errorf("unsupported login flow %q: expected %q, %q or %q", strings.TrimSpace(flow), ERunLoginFlowAuto, ERunLoginFlowDevice, ERunLoginFlowAuthCode)
	}
}
