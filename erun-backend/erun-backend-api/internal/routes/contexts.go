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
	eruncommon "github.com/sophium/erun/erun-common"
)

type ContextRepository interface {
	List(ctx context.Context) ([]model.Context, error)
	Get(ctx context.Context, contextID string) (model.Context, error)
	Create(ctx context.Context, cloudContext model.Context) (model.Context, error)
}

// ContextProvisioner starts the durable live provisioning of a freshly-created
// context. Optional: when nil, POST /v1/contexts only registers the row and
// returns the bootstrap plan (no live cluster bootstrap).
type ContextProvisioner interface {
	Start(provision.ProvisionInput) error
}

type ContextRoutes struct {
	contexts    ContextRepository
	provisioner ContextProvisioner
}

// createContextRequest is the BYO-cloud registration body for bootstrapping a
// managed cluster on the tenant's own AWS account.
type createContextRequest struct {
	Name               string `json:"name"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	Region             string `json:"region"`
	InstanceType       string `json:"instanceType"`
	DiskType           string `json:"diskType"`
	DiskSizeGB         int    `json:"diskSizeGb"`
	// MaxEnvironments is this context's placement capacity (#1112); zero uses
	// repository.DefaultContextMaxEnvironments.
	MaxEnvironments int  `json:"maxEnvironments"`
	Preview         bool `json:"preview"`
}

type createContextResponse struct {
	Context *model.Context `json:"context,omitempty"`
	Plan    []string       `json:"plan"`
}

// contextBootstrapParams are the trimmed cluster-bootstrap inputs. Both the
// context-create route and the provision preview plan from the same shape.
type contextBootstrapParams struct {
	name            string
	alias           string
	region          string
	instanceType    string
	diskType        string
	diskSizeGB      int
	maxEnvironments int
}

// createContextInput is a validated registration request.
type createContextInput struct {
	contextBootstrapParams
	preview bool
}

// decodeCreateContextInput validates the registration body. Its error message is
// the operator-facing 400 reason.
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
	// The name becomes the kubeconfig context label a deploy/stop/delete Job
	// interpolates into a shell-quoted kubectl argv (deployexec.PlacementParams)
	// and a Kubernetes label value; a DNS-1123 label keeps both safe, the same
	// constraint environment names already carry.
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
	cloudContext, err := r.contexts.Get(req.Context(), req.PathValue("context_id"))
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
		// A dry-run InitCloudContext failure here is an input-resolution error
		// (e.g. an unsupported region/instance type), not a server fault.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if input.preview {
		writeJSON(w, http.StatusOK, createContextResponse{Plan: plan})
		return
	}

	created, err := r.contexts.Create(req.Context(), model.Context{
		Name:               input.name,
		Provider:           eruncommon.CloudProviderAWS,
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

	// Live bootstrap runs for minutes: when a provisioner is wired, start it
	// durably and return 202 so the caller can poll GET /v1/contexts/{id};
	// otherwise the context is only registered (201).
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

// buildContextBootstrapPlan returns the cluster-bootstrap plan — the commands the
// real bootstrap would run — via a dry-run that never reaches AWS.
func buildContextBootstrapPlan(bootstrap contextBootstrapParams) ([]string, error) {
	var trace bytes.Buffer
	logger := eruncommon.NewLoggerWithWriters(eruncommon.VerbosityInfo, io.Discard, io.Discard).WithTraceSink(&trace)
	ctx := eruncommon.Context{
		Logger: logger,
		DryRun: true,
		Stdout: io.Discard,
		Stderr: io.Discard,
	}

	store := newInMemoryCloudStore(bootstrap.alias)
	params := eruncommon.InitCloudContextParams{
		Name:               bootstrap.name,
		CloudProviderAlias: bootstrap.alias,
		Region:             bootstrap.region,
		InstanceType:       bootstrap.instanceType,
		DiskType:           bootstrap.diskType,
		DiskSizeGB:         bootstrap.diskSizeGB,
	}
	// Zero-value dependencies: InitCloudContext normalizes them to its own
	// defaults, and in DryRun the default RunAWS/RunKubectl only trace the argv
	// (they never invoke the real CLIs).
	if _, err := eruncommon.InitCloudContext(ctx, store, params, eruncommon.CloudContextDependencies{}); err != nil {
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

// inMemoryCloudStore lets the dry-run InitCloudContext resolve the caller's AWS
// alias without touching disk or AWS; the dry-run never persists, so
// SaveERunConfig is unused.
type inMemoryCloudStore struct {
	config eruncommon.ERunConfig
}

func newInMemoryCloudStore(alias string) *inMemoryCloudStore {
	return &inMemoryCloudStore{
		config: eruncommon.ERunConfig{
			CloudProviders: []eruncommon.CloudProviderConfig{{
				Alias:    alias,
				Provider: eruncommon.CloudProviderAWS,
			}},
		},
	}
}

func (s *inMemoryCloudStore) LoadERunConfig() (eruncommon.ERunConfig, string, error) {
	return s.config, "", nil
}

func (s *inMemoryCloudStore) SaveERunConfig(config eruncommon.ERunConfig) error {
	s.config = config
	return nil
}
