package routes

import (
	"context"
	"errors"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

// MachineIdentityProvisioner provisions an environment's machine identity.
type MachineIdentityProvisioner interface {
	Provision(ctx context.Context, environmentID string) (service.MachineIdentityResult, error)
}

// MachineIdentityRoutes provisions the identity an environment authenticates as,
// so its calls are attributed to it rather than to an operator's session.
type MachineIdentityRoutes struct {
	identities MachineIdentityProvisioner
}

// machineIdentityResponse includes the client secret; it goes only to the caller.
type machineIdentityResponse struct {
	EnvironmentID   string `json:"environmentId"`
	EnvironmentName string `json:"environmentName"`
	Issuer          string `json:"issuer"`
	Subject         string `json:"subject"`
	ClientID        string `json:"clientId"`
	ClientSecret    string `json:"clientSecret"`
	UserID          string `json:"userId"`
	AlreadyEnrolled bool   `json:"alreadyEnrolled"`
}

// RegisterMachineIdentityRoutes registers even when unconfigured, answering 501
// rather than 404 so a deployment gap is distinguishable from a typo.
func RegisterMachineIdentityRoutes(register ProtectedRouteRegistrar, identities MachineIdentityProvisioner) {
	routes := MachineIdentityRoutes{identities: identities}
	register(http.MethodPost, "/v1/environments/{environment_id}/machine-identity", http.HandlerFunc(routes.provisionMachineIdentity))
}

func (r MachineIdentityRoutes) provisionMachineIdentity(w http.ResponseWriter, req *http.Request) {
	result, err := r.identities.Provision(req.Context(), req.PathValue("environment_id"))
	if err != nil {
		writeMachineIdentityError(w, req, err)
		return
	}
	// 200 for an existing identity lets a caller tell it apart without diffing credentials.
	status := http.StatusCreated
	if result.AlreadyEnrolled {
		status = http.StatusOK
	}
	writeJSON(w, status, machineIdentityResponse{
		EnvironmentID:   result.EnvironmentID,
		EnvironmentName: result.EnvironmentName,
		Issuer:          result.Issuer,
		Subject:         result.Subject,
		ClientID:        result.ClientID,
		ClientSecret:    result.ClientSecret,
		UserID:          result.UserID,
		AlreadyEnrolled: result.AlreadyEnrolled,
	})
}

func writeMachineIdentityError(w http.ResponseWriter, req *http.Request, err error) {
	switch {
	case errors.Is(err, service.ErrMachineIdentityProviderUnavailable):
		writeErrorCode(w, http.StatusNotImplemented, "MACHINE_IDENTITY_UNCONFIGURED",
			"this control plane has no identity provider it administers, so it cannot mint an environment identity; configure the platform's identity provider (the same dependency the /v1/identity routes need)")
	case errors.Is(err, service.ErrMachineIdentityUnavailable):
		writeErrorCode(w, http.StatusConflict, "MACHINE_IDENTITY_UNAVAILABLE", err.Error())
	default:
		writeRepositoryError(w, req, err)
	}
}
