package model

// ObservedIngress is one Ingress's routed hosts and TLS secrets.
type ObservedIngress struct {
	Name  string               `json:"name"`
	Hosts []string             `json:"hosts,omitempty"`
	TLS   []ObservedIngressTLS `json:"tls,omitempty"`
	// Backends avoids re-deriving Service names from a convention repo-native charts break.
	Backends []ObservedIngressBackend `json:"backends,omitempty"`
}

type ObservedIngressBackend struct {
	Service string `json:"service"`
	Port    int    `json:"port,omitempty"`
}

type ObservedIngressTLS struct {
	Hosts      []string `json:"hosts,omitempty"`
	SecretName string   `json:"secretName,omitempty"`
}
