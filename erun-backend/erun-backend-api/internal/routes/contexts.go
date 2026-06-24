package routes

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	eruncommon "github.com/sophium/erun/erun-common"
)

type ContextRepository interface {
	List(ctx context.Context) ([]model.Context, error)
	Get(ctx context.Context, contextID string) (model.Context, error)
	Create(ctx context.Context, cloudContext model.Context) (model.Context, error)
}

type ContextRoutes struct {
	contexts ContextRepository
}

// createContextRequest is the BYO-cloud registration body: the operator-authored
// fields needed to bootstrap a managed cluster (cloud context) via the tenant's
// AWS alias. preview=true returns the bootstrap plan only, with no DB write and
// no execution.
type createContextRequest struct {
	Name               string `json:"name"`
	CloudProviderAlias string `json:"cloudProviderAlias"`
	Region             string `json:"region"`
	InstanceType       string `json:"instanceType"`
	DiskType           string `json:"diskType"`
	DiskSizeGB         int    `json:"diskSizeGb"`
	Preview            bool   `json:"preview"`
}

// createContextResponse pairs the persisted context row with the cluster-
// bootstrap plan (the EC2/k3s commands the real bootstrap would run). On a
// preview-only request Context is omitted and only Plan is returned.
type createContextResponse struct {
	Context *model.Context `json:"context,omitempty"`
	Plan    []string       `json:"plan"`
}

func RegisterContextRoutes(register ProtectedRouteRegistrar, contexts ContextRepository) {
	routes := ContextRoutes{contexts: contexts}
	register(http.MethodGet, "/v1/contexts", http.HandlerFunc(routes.listContexts))
	register(http.MethodPost, "/v1/contexts", http.HandlerFunc(routes.createContext))
	register(http.MethodGet, "/v1/contexts/{context_id}", http.HandlerFunc(routes.getContext))
}

func (r ContextRoutes) listContexts(w http.ResponseWriter, req *http.Request) {
	contexts, err := r.contexts.List(req.Context())
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, contexts)
}

func (r ContextRoutes) getContext(w http.ResponseWriter, req *http.Request) {
	cloudContext, err := r.contexts.Get(req.Context(), req.PathValue("context_id"))
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cloudContext)
}

// createContext registers a cloud context (cluster) for the caller's tenant and
// returns the cluster-bootstrap plan. It always builds the plan via the
// eruncommon.InitCloudContext dry-run; when preview=true it returns the plan
// only, otherwise it persists a pending-status context row and returns
// {context, plan}.
func (r ContextRoutes) createContext(w http.ResponseWriter, req *http.Request) {
	var body createContextRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	name := strings.TrimSpace(body.Name)
	alias := strings.TrimSpace(body.CloudProviderAlias)
	region := strings.TrimSpace(body.Region)
	if name == "" || alias == "" || region == "" {
		writeError(w, http.StatusBadRequest, "name, cloudProviderAlias, and region are required")
		return
	}

	plan, err := buildContextBootstrapPlan(body, name, alias, region)
	if err != nil {
		// A dry-run InitCloudContext failure here is an input-resolution error
		// (e.g. an unsupported region/instance type), not a server fault.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if body.Preview {
		writeJSON(w, http.StatusOK, createContextResponse{Plan: plan})
		return
	}

	created, err := r.contexts.Create(req.Context(), model.Context{
		Name:               name,
		Provider:           eruncommon.CloudProviderAWS,
		CloudProviderAlias: alias,
		Region:             region,
		InstanceType:       strings.TrimSpace(body.InstanceType),
		DiskType:           strings.TrimSpace(body.DiskType),
		DiskSizeGB:         body.DiskSizeGB,
		KubernetesContext:  name,
	})
	if err != nil {
		writeRepositoryError(w, err)
		return
	}

	// TODO(#659 live): execute the real bootstrap (InitCloudContext with
	// DryRun=false + RunAWS against the tenant's alias) + custody the k3s admin
	// token/kubeconfig server-side. Requires a live AWS account + cluster; not
	// executed in this build. The persisted row above stands at its
	// pending-style status (no instance_id / public_ip yet) until that real
	// bootstrap runs and writes the resolved instance identity + token custody.

	writeJSON(w, http.StatusCreated, createContextResponse{Context: &created, Plan: plan})
}

// buildContextBootstrapPlan runs eruncommon.InitCloudContext in dry-run mode and
// returns the captured trace lines — the EC2/k3s commands the real bootstrap
// would execute. The dry-run never reaches AWS: it is driven by an in-memory
// CloudStore seeded with a matching AWS alias (InitCloudContext resolves the
// provider from the store before emitting any plan) and zero-value
// dependencies, so the helpers fall back to their deterministic dry-run output.
func buildContextBootstrapPlan(body createContextRequest, name, alias, region string) ([]string, error) {
	var trace bytes.Buffer
	logger := eruncommon.NewLoggerWithWriters(eruncommon.VerbosityInfo, io.Discard, io.Discard).WithTraceSink(&trace)
	ctx := eruncommon.Context{
		Logger: logger,
		DryRun: true,
		Stdout: io.Discard,
		Stderr: io.Discard,
	}

	store := newInMemoryCloudStore(alias)
	params := eruncommon.InitCloudContextParams{
		Name:               name,
		CloudProviderAlias: alias,
		Region:             region,
		InstanceType:       strings.TrimSpace(body.InstanceType),
		DiskType:           strings.TrimSpace(body.DiskType),
		DiskSizeGB:         body.DiskSizeGB,
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

// inMemoryCloudStore is a minimal eruncommon.CloudStore backed by an in-memory
// ERunConfig. It exists only so the dry-run InitCloudContext can resolve the
// caller's AWS alias and accumulate the plan without touching disk or AWS; the
// dry-run never persists, so SaveERunConfig is retained in memory and not used.
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
