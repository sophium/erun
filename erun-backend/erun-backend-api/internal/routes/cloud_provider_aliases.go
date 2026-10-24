package routes

import (
	"context"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
)

// CloudProviderAliasWriter stores a tenant's BYO-cloud credentials, encrypted at rest.
type CloudProviderAliasWriter interface {
	Set(ctx context.Context, alias, provider, credentials string) error
}

type CloudProviderAliasRoutes struct {
	aliases CloudProviderAliasWriter
}

type setCloudProviderAliasRequest struct {
	Provider    string `json:"provider"`
	Credentials string `json:"credentials"`
}

func RegisterCloudProviderAliasRoutes(register ProtectedRouteRegistrar, aliases CloudProviderAliasWriter) {
	routes := CloudProviderAliasRoutes{aliases: aliases}
	register(http.MethodPut, "/v1/cloud-provider-aliases/{alias}", http.HandlerFunc(routes.setAlias))
}

// No operations gate: RLS binds each alias row to the caller.
func (r CloudProviderAliasRoutes) setAlias(w http.ResponseWriter, req *http.Request) {
	if r.aliases == nil {
		writeError(w, http.StatusNotImplemented, "cloud provider alias storage is not configured (ERUN_SECRETS_KEY is unset)")
		return
	}
	alias := strings.TrimSpace(req.PathValue("alias"))
	if alias == "" {
		writeError(w, http.StatusBadRequest, "alias is required")
		return
	}
	var body setCloudProviderAliasRequest
	if err := decodeJSON(req, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	provider := strings.TrimSpace(body.Provider)
	if provider == "" {
		provider = config.CloudProviderAWS
	}
	if provider != config.CloudProviderAWS {
		writeError(w, http.StatusBadRequest, "provider must be aws")
		return
	}
	if strings.TrimSpace(body.Credentials) == "" {
		writeError(w, http.StatusBadRequest, "credentials are required")
		return
	}
	if err := r.aliases.Set(req.Context(), alias, provider, body.Credentials); err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
