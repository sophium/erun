package cloud

import (
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/cloudstore"
	"github.com/sophium/erun/erun-common/infra/cloud/secretstore"
)

type AWSIdentity struct {
	Account string `json:"Account"`
	Arn     string `json:"Arn"`
	UserID  string `json:"UserId"`
}

type CloudDependencies struct {
	RunAWSConfigureSSO      func(core.Context, AWSProfileConfig) error
	RunAWSLogin             func(core.Context, string) error
	RunAWSLogout            func(core.Context, string) error
	RunAWSBearerToken       func(core.Context, string, string) (string, error)
	RunAWSEnableOIDC        func(core.Context, string) (string, error)
	RunAWSExportCredentials func(core.Context, string) (CloudProviderCredentials, error)
	ResolveAWSIdentity      func(core.Context, string) (AWSIdentity, error)
	CheckAWSStatus          func(core.Context, model.CloudProviderConfig) model.CloudProviderStatus

	// CloudSecretStore is nil unless a transport wires one.
	VerifyCloudflareToken  func(core.Context, string) (CloudflareTokenInfo, error)
	ListCloudflareAccounts func(core.Context, string) ([]CloudflareAccount, error)
	CloudSecretStore       cloudstore.CloudSecretStore

	FetchPlatformInfo            func(core.Context, string) (model.PlatformInfo, error)
	FetchOIDCDiscovery           func(core.Context, string) (OIDCDiscovery, error)
	StartERunDeviceAuthorization func(core.Context, OIDCDiscovery, string, string) (ERunDeviceAuthorization, error)
	PollERunDeviceToken          func(core.Context, OIDCDiscovery, string, ERunDeviceAuthorization) (ERunTokens, error)
	RunERunAuthCodeLogin         func(core.Context, OIDCDiscovery, string, string) (ERunTokens, error)
	RefreshERunTokens            func(core.Context, OIDCDiscovery, string, string) (ERunTokens, error)
	ClientCredentialsERunTokens  func(core.Context, OIDCDiscovery, string, string) (ERunTokens, error)

	// FetchVersionJSON reads a surface's unauthenticated /version.json.
	FetchVersionJSON func(core.Context, string) (string, error)
	// ResolveHostAddrs lets drift detection tell a benign canonical alias from a different backend.
	ResolveHostAddrs func(core.Context, string) ([]string, error)
}

// DefaultCloudDependencies sets CloudSecretStore when the default store can be built and
// leaves it nil otherwise; every transport must use it.
func DefaultCloudDependencies() CloudDependencies {
	deps := CloudDependencies{}
	if store, err := secretstore.DefaultCloudSecretStore(); err == nil {
		deps.CloudSecretStore = store
	}
	return deps
}

// CloudProviderCredentials are temporary (~1h) AWS credentials for a remote runtime;
// callers must refresh.
type CloudProviderCredentials struct {
	Alias           string    `json:"alias"`
	AccessKeyID     string    `json:"accessKeyId"`
	SecretAccessKey string    `json:"secretAccessKey"`
	SessionToken    string    `json:"sessionToken"`
	Expiration      time.Time `json:"expiration"`
}

type AWSProfileConfig struct {
	Profile     string
	SSOStartURL string
	SSORegion   string
	AccountID   string
	RoleName    string
	Region      string
}
