package routes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

var (
	errMissingDefinitionPayload = errors.New("definition is required")
	errDefinitionNotAnObject    = errors.New("definition must be a JSON object")
)

// EnvironmentDefinitionRepository is the persistence access
// EnvironmentDefinitionRoutes needs: storing an environment's current
// definition and reading it back.
type EnvironmentDefinitionRepository interface {
	Upsert(ctx context.Context, environmentID string, definition json.RawMessage) (model.EnvironmentDefinition, error)
	Get(ctx context.Context, environmentID string) (model.EnvironmentDefinition, error)
}

type EnvironmentDefinitionRoutes struct {
	definitions  EnvironmentDefinitionRepository
	environments EnvironmentGetter
}

// RegisterEnvironmentDefinitionRoutes wires the environment definition store:
// the portable subset of an environment's config, uploaded from the machine
// that authored it and read back by a machine pulling the environment down.
//
// The platform stores the payload and hands it back without interpreting it.
// Deciding what may travel is the authoring client's job — erun-common's
// allowlist — because the field classes belong to the config type the client
// owns, and a second opinion here would be a second place for the two to
// disagree. What this layer does enforce is that the payload is a JSON object:
// a definition that is a bare array or scalar is not a definition, and a pull
// reading one would fail far from the write that stored it.
func RegisterEnvironmentDefinitionRoutes(register ProtectedRouteRegistrar, definitions EnvironmentDefinitionRepository, environments EnvironmentGetter) {
	routes := EnvironmentDefinitionRoutes{definitions: definitions, environments: environments}
	register(http.MethodPut, "/v1/environments/{environment_id}/definition", http.HandlerFunc(routes.putEnvironmentDefinition))
	register(http.MethodGet, "/v1/environments/{environment_id}/definition", http.HandlerFunc(routes.getEnvironmentDefinition))
}

// putEnvironmentDefinitionRequest carries the payload as raw JSON so the
// platform never has to model it. Absent is refused rather than stored as
// null: an upload with no definition has nothing to say.
type putEnvironmentDefinitionRequest struct {
	Definition json.RawMessage `json:"definition"`
}

// putEnvironmentDefinition stores this environment's current definition,
// advancing its revision by one.
func (r EnvironmentDefinitionRoutes) putEnvironmentDefinition(w http.ResponseWriter, req *http.Request) {
	environmentID := req.PathValue("environment_id")
	if _, err := r.environments.Get(req.Context(), environmentID); err != nil {
		writeRepositoryError(w, req, err)
		return
	}

	var body putEnvironmentDefinitionRequest
	if err := decodeJSON(req, &body); err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_BODY", err.Error())
		return
	}
	definition, err := normalizeDefinitionPayload(body.Definition)
	if err != nil {
		writeErrorCode(w, http.StatusBadRequest, "INVALID_DEFINITION", err.Error())
		return
	}

	stored, err := r.definitions.Upsert(req.Context(), environmentID, definition)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, stored)
}

// getEnvironmentDefinition returns the environment's stored definition and the
// revision it carries. A missing definition is a 404 naming that, not an empty
// payload: a caller that has never uploaded one and a caller whose upload was
// lost are different situations, and only one of them is answered by uploading.
func (r EnvironmentDefinitionRoutes) getEnvironmentDefinition(w http.ResponseWriter, req *http.Request) {
	environmentID := req.PathValue("environment_id")
	if _, err := r.environments.Get(req.Context(), environmentID); err != nil {
		writeRepositoryError(w, req, err)
		return
	}

	stored, err := r.definitions.Get(req.Context(), environmentID)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, stored)
}

// normalizeDefinitionPayload accepts only a JSON object, and normalizes absent
// to the same refusal as malformed.
func normalizeDefinitionPayload(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, errMissingDefinitionPayload
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, errDefinitionNotAnObject
	}
	if object == nil {
		return nil, errDefinitionNotAnObject
	}
	return raw, nil
}
