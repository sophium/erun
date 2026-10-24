package model

import (
	"time"
)

// PlatformInfo mirrors GET /v1/platform; duplicated because the backend's type is internal.
type PlatformInfo struct {
	Issuer     string `json:"issuer"`
	APIURL     string `json:"apiUrl"`
	ConsoleURL string `json:"consoleUrl"`
	// DocsURL is empty when the plane advertises no docs site.
	DocsURL         string `json:"docsUrl"`
	ConsoleClientID string `json:"consoleClientId"`
	CLIClientID     string `json:"cliClientId"`
	Brand           string `json:"brand"`
	// Version is "dev" for an unstamped build.
	Version string `json:"version"`
}

// PlatformWhoami mirrors GET /v1/whoami. Clients gate on Capabilities; Roles is descriptive.
type PlatformWhoami struct {
	TenantID   string   `json:"tenantId"`
	TenantName string   `json:"tenantName,omitempty"`
	UserID     string   `json:"userId"`
	Username   string   `json:"username,omitempty"`
	Roles      []string `json:"roles,omitempty"`
	// Capabilities is nil when unknown and empty when the caller may do nothing.
	Capabilities PlatformCapabilities `json:"capabilities"`
	Issuer       string               `json:"issuer"`
	Subject      string               `json:"subject"`
}

// PlatformTenant mirrors model.Tenant's JSON shape.
type PlatformTenant struct {
	TenantID  string    `json:"tenantId"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// Resolvable is set only by the operations-scoped listing; nil means not asked.
	Resolvable *bool `json:"resolvable,omitempty"`
}

// PlatformUser mirrors model.User's JSON shape.
type PlatformUser struct {
	UserID   string `json:"userId"`
	TenantID string `json:"tenantId"`
	Username string `json:"username"`
	Issuer   string `json:"issuer,omitempty"`
	Subject  string `json:"subject,omitempty"`
	// AlreadyEnrolled reports that POST /v1/users found the identity already enrolled.
	AlreadyEnrolled bool      `json:"alreadyEnrolled,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// PlatformEnvironment mirrors model.Environment's JSON shape.
type PlatformEnvironment struct {
	EnvironmentID     string `json:"environmentId"`
	TenantID          string `json:"tenantId"`
	Name              string `json:"name"`
	Type              string `json:"type"`
	KubernetesContext string `json:"kubernetesContext,omitempty"`
	ContextID         string `json:"contextId,omitempty"`
	RuntimeVersion    string `json:"runtimeVersion,omitempty"`
	Status            string `json:"status"`
	ProvisionError    string `json:"provisionError,omitempty"`
	DeployedVersion   string `json:"deployedVersion,omitempty"`
	// DeleteError holds the namespace's conditions when Status is "deletion-blocked".
	DeleteError string    `json:"deleteError,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// PlatformContext mirrors model.Context's JSON shape.
type PlatformContext struct {
	ContextID          string    `json:"contextId"`
	TenantID           string    `json:"tenantId"`
	Name               string    `json:"name"`
	Provider           string    `json:"provider"`
	CloudProviderAlias string    `json:"cloudProviderAlias,omitempty"`
	Region             string    `json:"region,omitempty"`
	InstanceID         string    `json:"instanceId,omitempty"`
	PublicIP           string    `json:"publicIp,omitempty"`
	InstanceType       string    `json:"instanceType,omitempty"`
	DiskType           string    `json:"diskType,omitempty"`
	DiskSizeGB         int       `json:"diskSizeGb,omitempty"`
	KubernetesContext  string    `json:"kubernetesContext,omitempty"`
	Status             string    `json:"status"`
	ProvisionError     string    `json:"provisionError,omitempty"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}
