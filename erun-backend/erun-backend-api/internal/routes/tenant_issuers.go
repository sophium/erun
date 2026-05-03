package routes

import (
	"context"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

type TenantIssuerRepository interface {
	List(ctx context.Context) ([]model.TenantIssuer, error)
	UpdateName(ctx context.Context, issuer string, name string) (model.TenantIssuer, error)
}

type TenantIssuerRoutes struct {
	issuers TenantIssuerRepository
}

type updateTenantIssuerRequest struct {
	Issuer string `json:"issuer"`
	Name   string `json:"name"`
}

func RegisterTenantIssuerRoutes(register ProtectedRouteRegistrar, issuers TenantIssuerRepository) {
	routes := TenantIssuerRoutes{issuers: issuers}
	register(http.MethodGet, "/v1/tenant-issuers", http.HandlerFunc(routes.listTenantIssuers))
	register(http.MethodPatch, "/v1/tenant-issuers", http.HandlerFunc(routes.updateTenantIssuerName))
}

func (r TenantIssuerRoutes) listTenantIssuers(w http.ResponseWriter, req *http.Request) {
	issuers, err := r.issuers.List(req.Context())
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, issuers)
}

func (r TenantIssuerRoutes) updateTenantIssuerName(w http.ResponseWriter, req *http.Request) {
	var input updateTenantIssuerRequest
	if err := decodeJSON(req, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	issuer, err := r.issuers.UpdateName(req.Context(), input.Issuer, input.Name)
	if err != nil {
		writeRepositoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, issuer)
}
