// Package provision runs the durable live provisioning of a cloud context: it
// executes erun's real InitCloudContext (DryRun=false) against the tenant's
// BYO-cloud alias, custodies the resulting k3s admin token, and records the
// context's provisioning lifecycle — all as a DBOS durable workflow so a
// control-plane restart resumes from the last completed step (issues #605/#676).
package provision

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/dbos-inc/dbos-transact-golang/dbos"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/secrets"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	eruncommon "github.com/sophium/erun/erun-common"
)

// ProvisionInput is the serializable workflow input DBOS checkpoints. It carries
// only the tenant identity (to rebuild the RLS security context inside each
// step) and the operator-authored bootstrap parameters — never a secret.
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

// bootstrapResult is the non-secret output of the bootstrap step. The k3s admin
// token is custodied (encrypted) inside the step and never returned, so it is
// never written to DBOS's plaintext step-output checkpoint.
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
	// awsEndpoint, when set, points every aws call at a local emulator (floci)
	// for verification; empty means the real AWS endpoints.
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
	// A method value: a stable workflow name across restarts (so DBOS recovers
	// it) that also captures p's dependencies. Registered once here and reused
	// by Start, so RegisterWorkflow and RunWorkflow see the same function.
	p.workflowFn = p.provisionWorkflow
	dbos.RegisterWorkflow(dbosCtx, p.workflowFn)
	return p
}

// Start kicks off provisioning asynchronously. The HTTP handler returns
// immediately; the durable workflow drives the (minutes-long) bootstrap and
// updates the context's status. The context_id is the idempotency key, so a
// retried request does not start a second bootstrap.
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

// bootstrapAndCustody resolves the tenant's alias, runs the real EC2+k3s
// bootstrap, and custodies the k3s admin token (encrypted) — all within one
// step so the token never becomes a DBOS step-output checkpoint.
func (p *Provisioner) bootstrapAndCustody(c context.Context, input ProvisionInput) (bootstrapResult, error) {
	sc := p.scoped(c, input)
	alias, err := p.aliases.Get(sc, input.CloudProviderAlias)
	if err != nil {
		return bootstrapResult{}, fmt.Errorf("resolve cloud provider alias %q: %w", input.CloudProviderAlias, err)
	}
	store := aliasStore{alias: alias.Alias, provider: alias.Provider}
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
		RunAWS:     p.awsRunner(c, alias.Credentials),
		RunKubectl: func(eruncommon.Context, []string) error { return nil },
		// Deterministic k3s admin token derived from the context id: a re-run
		// (durable workflow resuming after a crash, reusing the existing tagged
		// instance) re-derives the SAME token the instance baked, so custody is
		// idempotent. Domain-separated from encryption; never stored in the DBOS
		// checkpoint.
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

// awsRunner returns a CloudContextDependencies.RunAWS that shells the aws CLI
// with the tenant's decrypted credentials, optionally pinned at a local emulator
// endpoint (floci) for verification.
func (p *Provisioner) awsRunner(ctx context.Context, credentialsJSON string) func(eruncommon.Context, eruncommon.CloudProviderConfig, string, []string) (string, error) {
	return func(_ eruncommon.Context, _ eruncommon.CloudProviderConfig, region string, args []string) (string, error) {
		argv := []string{"--region", region}
		if p.awsEndpoint != "" {
			argv = append(argv, "--endpoint-url", p.awsEndpoint)
		}
		argv = append(argv, args...)
		cmd := exec.CommandContext(ctx, "aws", argv...)
		cmd.Env = awsEnv(credentialsJSON)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return string(out), fmt.Errorf("aws %v: %w: %s", args, err, string(out))
		}
		return string(out), nil
	}
}

type awsCredentials struct {
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	SessionToken    string `json:"sessionToken,omitempty"`
}

func awsEnv(credentialsJSON string) []string {
	env := os.Environ()
	var creds awsCredentials
	if err := json.Unmarshal([]byte(credentialsJSON), &creds); err != nil {
		return env
	}
	if creds.AccessKeyID != "" {
		env = append(env, "AWS_ACCESS_KEY_ID="+creds.AccessKeyID)
	}
	if creds.SecretAccessKey != "" {
		env = append(env, "AWS_SECRET_ACCESS_KEY="+creds.SecretAccessKey)
	}
	if creds.SessionToken != "" {
		env = append(env, "AWS_SESSION_TOKEN="+creds.SessionToken)
	}
	return env
}

// aliasStore is the minimal eruncommon.CloudContextStore InitCloudContext needs
// to resolve the provider for an alias. It never persists (SaveERunConfig is a
// no-op): the provisioner captures InitCloudContext's returned identity and
// writes it to the contexts table itself.
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
