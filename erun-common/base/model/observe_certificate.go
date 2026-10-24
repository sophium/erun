package model

// ObservedCertificate is a Certificate's readiness; Orders is populated only when Ready is false.
type ObservedCertificate struct {
	Name       string                     `json:"name"`
	Ready      bool                       `json:"ready"`
	Reason     string                     `json:"reason,omitempty"`
	Message    string                     `json:"message,omitempty"`
	SecretName string                     `json:"secretName,omitempty"`
	DNSNames   []string                   `json:"dnsNames,omitempty"`
	Orders     []ObservedCertificateOrder `json:"orders,omitempty"`
}

type ObservedCertificateOrder struct {
	Name       string                         `json:"name"`
	State      string                         `json:"state,omitempty"`
	Reason     string                         `json:"reason,omitempty"`
	Challenges []ObservedCertificateChallenge `json:"challenges,omitempty"`
}

type ObservedCertificateChallenge struct {
	Name    string `json:"name"`
	Type    string `json:"type,omitempty"`
	DNSName string `json:"dnsName,omitempty"`
	State   string `json:"state,omitempty"`
	Reason  string `json:"reason,omitempty"`
}
