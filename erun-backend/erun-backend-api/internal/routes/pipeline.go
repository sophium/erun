package routes

import (
	"context"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

// PipelineBuilder is the pipeline view's own read. It is an interface here so
// the route can be exercised without a database, and so a handler that
// stopped wiring the union would fail to compile rather than answer an empty
// view.
type PipelineBuilder interface {
	Build(ctx context.Context) ([]service.PipelineIssue, error)
}

type PipelineRoutes struct {
	pipeline PipelineBuilder
}

// RegisterPipelineRoutes wires the one read that spans both pipelines: the
// jobs recording planned and in-flight work, and the reviews moving through
// the merge queue, unioned on the issue each belongs to.
//
// It is a read and only a read — no state machine changes here, and nothing
// this route touches review_merge_queue, whose head query a merge driver
// acts on.
func RegisterPipelineRoutes(register ProtectedRouteRegistrar, pipeline PipelineBuilder) {
	routes := PipelineRoutes{pipeline: pipeline}
	register(http.MethodGet, "/v1/pipeline", http.HandlerFunc(routes.getPipeline))
}

// getPipeline answers GET /v1/pipeline with the caller's tenant's work,
// grouped by issue and labelled by rung.
//
// An empty result is a definite one — a tenant with nothing recorded is a
// valid answer, not a failure — so the list is built non-nil and marshals as
// [] rather than null, the same contract every list on this API holds to.
func (r PipelineRoutes) getPipeline(w http.ResponseWriter, req *http.Request) {
	issues, err := r.pipeline.Build(req.Context())
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	if issues == nil {
		issues = []service.PipelineIssue{}
	}
	writeJSON(w, http.StatusOK, issues)
}
