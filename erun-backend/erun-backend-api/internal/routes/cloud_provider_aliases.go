package routes

import (
	"context"
	"net/http"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// CloudProviderAliasWriter stores a tenant's BYO-cloud credentials. The
// credentials blob is opaque to the API and encrypted at rest.
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

// setAlias needs no operations gate: RLS binds each alias row to the caller, so
// every authorized tenant manages only its own aliases.
func (r CloudProviderAliasRoutes) setAlias(w http.ResponseWriter, req *http.Request) {
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
		provider = eruncommon.CloudProviderAWS
	}
	if provider != eruncommon.CloudProviderAWS {
		writeError(w, http.StatusBadRequest, "provider must be aws")
		return
	}
	if strings.TrimSpace(body.Credentials) == "" {
		writeError(w, http.StatusBadRequest, "credentials are required")
		return
	}
	if err := r.aliases.Set(req.Context(), alias, provider, body.Credentials); err != nil {
		writeRepositoryError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
