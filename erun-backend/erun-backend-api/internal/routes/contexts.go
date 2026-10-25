package routes

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/provision"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	modelpkg "github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud/cloudcontext"
	"github.com/sophium/erun/erun-common/infra/cloud/contextinit"
)

type ContextRepository interface {
	List(ctx context.Context) ([]model.Context, error)
	// Get is tenant-scoped because erun_operations' RLS policy is unconditional.
	Get(ctx context.Context, tenantID, contextID string) (model.Context, error)
	Create(ctx context.Context, cloudContext model.Context) (model.Context, error)
}

// ContextProvisioner starts live provisioning of a new context. When nil,
// POST /v1/contexts only registers the row and returns the plan.
type ContextProvisioner interface {
	Start(provision.ProvisionInput) error
}

type ContextRoutes struct {
	contexts    ContextRepository
	provisioner ContextProvisioner
}

// createContextRequest is the BYO-cloud cluster registration body.
type createContextRequest struct {
	Name               string `json:"name"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	Region             string `json:"region"`
	InstanceType       string `json:"instanceType"`
	DiskType           string `json:"diskType"`
	DiskSizeGB         int    `json:"diskSizeGb"`
	// Zero uses repository.DefaultContextMaxEnvironments.
	MaxEnvironments int  `json:"maxEnvironments"`
	Preview         bool `json:"preview"`
}

type createContextResponse struct {
	Context *model.Context `json:"context,omitempty"`
	Plan    []string       `json:"plan"`
}

// contextBootstrapParams are the trimmed cluster-bootstrap inputs.
type contextBootstrapParams struct {
	name            string
	alias           string
	region          string
	instanceType    string
	diskType        string
	diskSizeGB      int
	maxEnvironments int
}

type createContextInput struct {
	contextBootstrapParams
	preview bool
}

// decodeCreateContextInput validates the body; errors are the 400 reason.
func decodeCreateContextInput(req *http.Request) (createContextInput, error) {
	var body createContextRequest
	if err := decodeJSON(req, &body); err != nil {
		return createContextInput{}, errors.New("invalid request body")
	}
	input := createContextInput{
		contextBootstrapParams: contextBootstrapParams{
			name:            strings.TrimSpace(body.Name),
			alias:           strings.TrimSpace(body.CloudProviderAlias),
			region:          strings.TrimSpace(body.Region),
			instanceType:    strings.TrimSpace(body.InstanceType),
			diskType:        strings.TrimSpace(body.DiskType),
			diskSizeGB:      body.DiskSizeGB,
			maxEnvironments: body.MaxEnvironments,
		},
		preview: body.Preview,
	}
	if input.name == "" || input.alias == "" || input.region == "" {
		return createContextInput{}, errors.New("name, cloudProviderAlias, and region are required")
	}
	// The name reaches a shell-quoted kubectl argv and a label value; DNS-1123 keeps both safe.
	if !validNamespaceLabel(input.name) {
		return createContextInput{}, errors.New("name must be a DNS-1123 label: lowercase letters, digits, and internal hyphens, not starting or ending with a hyphen, at most 63 characters")
	}
	if input.maxEnvironments < 0 {
		return createContextInput{}, errors.New("maxEnvironments must not be negative")
	}
	return input, nil
}

func RegisterContextRoutes(register ProtectedRouteRegistrar, contexts ContextRepository, provisioner ContextProvisioner) {
	routes := ContextRoutes{contexts: contexts, provisioner: provisioner}
	register(http.MethodGet, "/v1/contexts", http.HandlerFunc(routes.listContexts))
	register(http.MethodPost, "/v1/contexts", http.HandlerFunc(routes.createContext))
	register(http.MethodGet, "/v1/contexts/{context_id}", http.HandlerFunc(routes.getContext))
}

func (r ContextRoutes) listContexts(w http.ResponseWriter, req *http.Request) {
	contexts, err := r.contexts.List(req.Context())
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, contexts)
}

func (r ContextRoutes) getContext(w http.ResponseWriter, req *http.Request) {
	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), errors.New("security context not found in request"))
		return
	}
	cloudContext, err := r.contexts.Get(req.Context(), securityContext.TenantID, req.PathValue("context_id"))
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, cloudContext)
}

func (r ContextRoutes) createContext(w http.ResponseWriter, req *http.Request) {
	input, err := decodeCreateContextInput(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	plan, err := buildContextBootstrapPlan(input.contextBootstrapParams)
	if err != nil {
		// A dry-run failure is an input error, not a server fault.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if input.preview {
		writeJSON(w, http.StatusOK, createContextResponse{Plan: plan})
		return
	}

	created, err := r.contexts.Create(req.Context(), model.Context{
		Name:               input.name,
		Provider:           configpkg.CloudProviderAWS,
		CloudProviderAlias: input.alias,
		Region:             input.region,
		InstanceType:       input.instanceType,
		DiskType:           input.diskType,
		DiskSizeGB:         input.diskSizeGB,
		KubernetesContext:  input.name,
		MaxEnvironments:    input.maxEnvironments,
	})
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}

	// Live bootstrap takes minutes, so a wired provisioner returns 202 for polling.
	if r.provisioner == nil {
		writeJSON(w, http.StatusCreated, createContextResponse{Context: &created, Plan: plan})
		return
	}

	securityContext, ok := security.FromContext(req.Context())
	if !ok {
		writeInternalError(w, req, http.StatusText(http.StatusInternalServerError), errors.New("security context not found in request"))
		return
	}
	if err := r.provisioner.Start(provision.ProvisionInput{
		TenantID:           securityContext.TenantID,
		TenantType:         securityContext.TenantType,
		ErunUserID:         securityContext.ErunUserID,
		ContextID:          created.ContextID,
		Name:               input.name,
		CloudProviderAlias: input.alias,
		Region:             input.region,
		InstanceType:       input.instanceType,
		DiskType:           input.diskType,
		DiskSizeGB:         input.diskSizeGB,
	}); err != nil {
		writeInternalError(w, req, "failed to start provisioning", err)
		return
	}
	writeJSON(w, http.StatusAccepted, createContextResponse{Context: &created, Plan: plan})
}

// buildContextBootstrapPlan returns the bootstrap commands via a dry-run that never reaches AWS.
func buildContextBootstrapPlan(bootstrap contextBootstrapParams) ([]string, error) {
	var trace bytes.Buffer
	logger := core.NewLoggerWithWriters(core.VerbosityInfo, io.Discard, io.Discard).WithTraceSink(&trace)
	ctx := core.Context{
		Logger: logger,
		DryRun: true,
		Stdout: io.Discard,
		Stderr: io.Discard,
	}

	store := newInMemoryCloudStore(bootstrap.alias)
	params := modelpkg.InitCloudContextParams{
		Name:               bootstrap.name,
		CloudProviderAlias: bootstrap.alias,
		Region:             bootstrap.region,
		InstanceType:       bootstrap.instanceType,
		DiskType:           bootstrap.diskType,
		DiskSizeGB:         bootstrap.diskSizeGB,
	}
	// In DryRun the default runners only trace argv.
	if _, err := contextinit.InitCloudContext(ctx, store, params, cloudcontext.CloudContextDependencies{}); err != nil {
		return nil, err
	}
	return splitTraceLines(trace.String()), nil
}

func splitTraceLines(trace string) []string {
	lines := make([]string, 0)
	for _, line := range strings.Split(trace, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

// inMemoryCloudStore resolves the AWS alias for the dry-run without touching disk.
type inMemoryCloudStore struct {
	config configpkg.ERunConfig
}

func newInMemoryCloudStore(alias string) *inMemoryCloudStore {
	return &inMemoryCloudStore{
		config: configpkg.ERunConfig{
			CloudProviders: []modelpkg.CloudProviderConfig{{
				Alias:    alias,
				Provider: configpkg.CloudProviderAWS,
			}},
		},
	}
}

func (s *inMemoryCloudStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return s.config, "", nil
}

func (s *inMemoryCloudStore) SaveERunConfig(config configpkg.ERunConfig) error {
	s.config = config
	return nil
}
