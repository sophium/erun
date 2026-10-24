package model

// RuntimeAgentCredentialState is the verdict on the pod's model-provider wiring.
type RuntimeAgentCredentialState string

const (
	// RuntimeAgentCredentialNotApplicable asserts nothing: an env no gateway routes may reach a provider otherwise.
	RuntimeAgentCredentialNotApplicable RuntimeAgentCredentialState = "not-applicable"
	RuntimeAgentCredentialConfigured    RuntimeAgentCredentialState = "configured"
	// RuntimeAgentCredentialMissing: a gateway routes the env but the template lacks the routing variable.
	RuntimeAgentCredentialMissing RuntimeAgentCredentialState = "missing"
	// RuntimeAgentCredentialUnknown is never reported as Missing: an unread template is not evidence of absence.
	RuntimeAgentCredentialUnknown RuntimeAgentCredentialState = "unknown"
)

// RuntimeAgentCredentialStatus answers whether the runtime pod can reach the configured provider.
type RuntimeAgentCredentialStatus struct {
	State RuntimeAgentCredentialState `json:"state"`
	// GatewayBaseURL is a routing address, never a credential.
	GatewayBaseURL string `json:"gatewayBaseURL,omitempty"`
	Container      string `json:"container,omitempty"`
	// ReadError is set only when the template could not be observed, never for a genuinely empty one.
	ReadError string `json:"readError,omitempty"`
}
