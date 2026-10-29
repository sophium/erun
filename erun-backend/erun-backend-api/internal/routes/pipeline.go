package routes

import (
	"context"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

// PipelineBuilder builds the pipeline view.
type PipelineBuilder interface {
	Build(ctx context.Context) ([]service.PipelineIssue, error)
}

type PipelineRoutes struct {
	pipeline PipelineBuilder
}

// RegisterPipelineRoutes wires the read-only union of jobs and merge-queue
// reviews, grouped by issue.
func RegisterPipelineRoutes(register ProtectedRouteRegistrar, pipeline PipelineBuilder) {
	routes := PipelineRoutes{pipeline: pipeline}
	register(http.MethodGet, "/v1/pipeline", http.HandlerFunc(routes.getPipeline))
}

// getPipeline returns [] rather than null for an empty tenant.
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
