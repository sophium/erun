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

// EnvironmentDefinitionRepository stores and reads an environment's definition.
type EnvironmentDefinitionRepository interface {
	Upsert(ctx context.Context, environmentID string, definition json.RawMessage) (model.EnvironmentDefinition, error)
	Get(ctx context.Context, environmentID string) (model.EnvironmentDefinition, error)
}

type EnvironmentDefinitionRoutes struct {
	definitions  EnvironmentDefinitionRepository
	environments EnvironmentGetter
}

// RegisterEnvironmentDefinitionRoutes wires the environment definition store.
// The payload is opaque here: what may travel is the client allowlist's call. Only
// "is a JSON object" is enforced, so a bad definition fails at write, not at pull.
func RegisterEnvironmentDefinitionRoutes(register ProtectedRouteRegistrar, definitions EnvironmentDefinitionRepository, environments EnvironmentGetter) {
	routes := EnvironmentDefinitionRoutes{definitions: definitions, environments: environments}
	register(http.MethodPut, "/v1/environments/{environment_id}/definition", http.HandlerFunc(routes.putEnvironmentDefinition))
	register(http.MethodGet, "/v1/environments/{environment_id}/definition", http.HandlerFunc(routes.getEnvironmentDefinition))
}

type putEnvironmentDefinitionRequest struct {
	Definition json.RawMessage `json:"definition"`
}

// putEnvironmentDefinition stores the definition, advancing its revision by one.
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

// getEnvironmentDefinition returns 404 for a missing definition, not an empty payload.
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
