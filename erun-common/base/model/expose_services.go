package model

// EnvironmentService is one Service in an environment's namespace with its
// ports and any existing exposure.
type EnvironmentService struct {
	Name     string                `json:"name"`
	Type     string                `json:"type,omitempty"`
	Ports    []ObservedServicePort `json:"ports,omitempty"`
	Exposure *ServiceExposure      `json:"exposure,omitempty"`
}

// ServiceExposure is an EnvironmentService's existing exposure; Label need not
// be the Service's name.
type ServiceExposure struct {
	Label    string `json:"label"`
	Hostname string `json:"hostname"`
	Scheme   string `json:"scheme"`
}

// EnvironmentServiceList is the shared CLI/MCP result of a services read.
type EnvironmentServiceList struct {
	Tenant      string               `json:"tenant"`
	Environment string               `json:"environment"`
	Namespace   string               `json:"namespace"`
	Services    []EnvironmentService `json:"services"`
}
