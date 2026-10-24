package model

// ObservedService is one Service in an environment's namespace.
type ObservedService struct {
	Name  string                `json:"name"`
	Type  string                `json:"type,omitempty"`
	Ports []ObservedServicePort `json:"ports,omitempty"`
}

// ObservedServicePort is one published port; Name is empty for a single-port Service.
type ObservedServicePort struct {
	Name     string `json:"name,omitempty"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol,omitempty"`
}
