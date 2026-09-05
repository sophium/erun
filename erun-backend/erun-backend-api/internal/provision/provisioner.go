// Package provision runs the durable live provisioning of a tenant's cloud
// context as a DBOS workflow, so a control-plane restart resumes from the last
// completed step instead of re-running the whole bootstrap.
package provision

import (
	"context"
	"fmt"
	"io"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/secrets"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	eruncommon "github.com/sophium/erun/erun-common"
)

// ProvisionInput is the workflow input DBOS checkpoints. Because the checkpoint
// is persisted, it carries only tenant identity and operator-authored bootstrap
// parameters — never a secret.
type ProvisionInput struct {
	TenantID           string `json:"tenantId"`
	TenantType         string `json:"tenantType"`
	ErunUserID         string `json:"erunUserId,omitempty"`
	ContextID          string `json:"contextId"`
	Name               string `json:"name"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	Region             string `json:"region"`
	InstanceType       string `json:"instanceType"`
	DiskType           string `json:"diskType"`
	DiskSizeGB         int    `json:"diskSizeGb"`
}

// ProvisionResult is the workflow output (non-secret).
type ProvisionResult struct {
	InstanceID string `json:"instanceId"`
	PublicIP   string `json:"publicIp"`
	Status     string `json:"status"`
}

// bootstrapResult is deliberately non-secret: the k3s admin token is custodied
// inside the step and never returned, so it never lands in DBOS's plaintext
// step-output checkpoint.
type bootstrapResult struct {
	InstanceID string `json:"instanceId"`
	PublicIP   string `json:"publicIp"`
}

const (
	statusRunning = "running"
	statusFailed  = "failed"
)

// Provisioner owns the durable provisioning workflow and its dependencies.
type Provisioner struct {
	dbosCtx     dbos.DBOSContext
	contexts    *repository.ContextRepository
	credentials *repository.ContextCredentialRepository
	aliases     *repository.CloudProviderAliasRepository
	// awsEndpoint pins aws calls at a local emulator (floci) for verification.
	awsEndpoint string
	cipher      *secrets.Cipher
	workflowFn  func(dbos.DBOSContext, ProvisionInput) (ProvisionResult, error)
}

// NewProvisioner builds the provisioner and registers its workflow with DBOS.
// Call before dbos.Launch.
func NewProvisioner(
	dbosCtx dbos.DBOSContext,
	contexts *repository.ContextRepository,
	credentials *repository.ContextCredentialRepository,
	aliases *repository.CloudProviderAliasRepository,
	cipher *secrets.Cipher,
	awsEndpoint string,
) *Provisioner {
	p := &Provisioner{
		dbosCtx:     dbosCtx,
		contexts:    contexts,
		credentials: credentials,
		aliases:     aliases,
		cipher:      cipher,
		awsEndpoint: awsEndpoint,
	}
	// Stored once so RegisterWorkflow (here) and RunWorkflow (in Start) share one
	// stable function value, which is how DBOS names the workflow and recovers it
	// across restarts.
	p.workflowFn = p.provisionWorkflow
	dbos.RegisterWorkflow(dbosCtx, p.workflowFn)
	return p
}

// Start kicks off provisioning asynchronously so the HTTP handler returns
// immediately while the durable workflow runs the minutes-long bootstrap. The
// context id is the idempotency key, so a retried request does not start a
// second bootstrap.
func (p *Provisioner) Start(input ProvisionInput) error {
	_, err := dbos.RunWorkflow(p.dbosCtx, p.workflowFn, input, dbos.WithWorkflowID("provision-"+input.ContextID))
	return err
}

func (p *Provisioner) provisionWorkflow(dctx dbos.DBOSContext, input ProvisionInput) (ProvisionResult, error) {
	boot, err := dbos.RunAsStep(dctx, func(c context.Context) (bootstrapResult, error) {
		return p.bootstrapAndCustody(c, input)
	})
	if err != nil {
		// Record the failure in its own checkpointed step so the console sees it
		// even if the workflow is not retried.
		_, _ = dbos.RunAsStep(dctx, func(c context.Context) (string, error) {
			return "", p.markFailed(c, input, err)
		})
		return ProvisionResult{}, err
	}
	if _, err := dbos.RunAsStep(dctx, func(c context.Context) (string, error) {
		return "", p.markRunning(c, input, boot)
	}); err != nil {
		return ProvisionResult{}, err
	}
	return ProvisionResult{InstanceID: boot.InstanceID, PublicIP: boot.PublicIP, Status: statusRunning}, nil
}

// bootstrapAndCustody does the bootstrap and token custody in a single DBOS step
// so the k3s admin token never becomes a step-output checkpoint.
func (p *Provisioner) bootstrapAndCustody(c context.Context, input ProvisionInput) (bootstrapResult, error) {
	sc := p.scoped(c, input)
	alias, err := p.aliases.Get(sc, input.CloudProviderAlias)
	if err != nil {
		return bootstrapResult{}, fmt.Errorf("resolve cloud provider alias %q: %w", input.CloudProviderAlias, err)
	}
	store := aliasStore{alias: alias.Alias, provider: alias.Provider}
	runner, err := newAWSSDKRunner(alias.Credentials, p.awsEndpoint)
	if err != nil {
		return bootstrapResult{}, fmt.Errorf("cloud provider alias %q: %w", input.CloudProviderAlias, err)
	}
	ectx := eruncommon.Context{
		Logger: eruncommon.NewLoggerWithWriters(eruncommon.VerbosityInfo, io.Discard, io.Discard),
		DryRun: false,
		Stdout: io.Discard,
		Stderr: io.Discard,
	}
	params := eruncommon.InitCloudContextParams{
		Name:               input.Name,
		CloudProviderAlias: input.CloudProviderAlias,
		Region:             input.Region,
		InstanceType:       input.InstanceType,
		DiskType:           input.DiskType,
		DiskSizeGB:         input.DiskSizeGB,
	}
	status, err := eruncommon.InitCloudContext(ectx, store, params, eruncommon.CloudContextDependencies{
		RunAWS:     runner.runAWS(c),
		RunKubectl: func(eruncommon.Context, []string) error { return nil },
		// Deterministic token from the context id: a crash-resumed re-run (reusing
		// the existing tagged instance) re-derives the SAME token the instance
		// already baked, so custody stays idempotent. Domain-separated from
		// encryption.
		NewToken: func() string { return p.cipher.DeriveToken("k3s-admin-token:" + input.ContextID) },
	})
	if err != nil {
		return bootstrapResult{}, fmt.Errorf("bootstrap cloud context: %w", err)
	}
	if err := p.credentials.Set(sc, input.ContextID, status.AdminToken); err != nil {
		return bootstrapResult{}, fmt.Errorf("custody k3s admin token: %w", err)
	}
	return bootstrapResult{InstanceID: status.InstanceID, PublicIP: status.PublicIP}, nil
}

func (p *Provisioner) markRunning(c context.Context, input ProvisionInput, boot bootstrapResult) error {
	return p.contexts.UpdateProvisioningResult(p.scoped(c, input), input.ContextID, statusRunning, boot.InstanceID, boot.PublicIP, "")
}

func (p *Provisioner) markFailed(c context.Context, input ProvisionInput, cause error) error {
	return p.contexts.UpdateProvisioningResult(p.scoped(c, input), input.ContextID, statusFailed, "", "", cause.Error())
}

// scoped rebuilds the request-scoped security context inside a workflow step so
// the repositories' RLS transaction wiring binds to the right tenant.
func (p *Provisioner) scoped(c context.Context, input ProvisionInput) context.Context {
	return security.WithContext(c, security.Context{
		TenantID:   input.TenantID,
		TenantType: input.TenantType,
		ErunUserID: input.ErunUserID,
	})
}

// aliasStore is the minimal CloudContextStore InitCloudContext needs to resolve
// the provider for an alias. SaveERunConfig is intentionally a no-op: the
// provisioner writes the returned identity to the contexts table itself.
type aliasStore struct {
	alias    string
	provider string
}

func (s aliasStore) LoadERunConfig() (eruncommon.ERunConfig, string, error) {
	return eruncommon.ERunConfig{
		CloudProviders: []eruncommon.CloudProviderConfig{{Alias: s.alias, Provider: s.provider}},
	}, "", nil
}

func (s aliasStore) SaveERunConfig(eruncommon.ERunConfig) error { return nil }
