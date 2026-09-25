package routes

import (
	"context"
	"errors"
	"net/http"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/service"
)

// MachineIdentityProvisioner is the workflow dependency for
// POST /v1/environments/{environment_id}/machine-identity.
type MachineIdentityProvisioner interface {
	Provision(ctx context.Context, environmentID string) (service.MachineIdentityResult, error)
}

// MachineIdentityRoutes provisions an environment's own platform identity: the
// credential the environment authenticates as, so its platform calls are
// attributable to the environment rather than to whichever operator's session
// `erun init` delegated to it.
type MachineIdentityRoutes struct {
	identities MachineIdentityProvisioner
}

// machineIdentityResponse is the provisioned identity, including the
// credential's confidential half. It is returned to the caller that asked for
// it and nowhere else: the caller places it in the environment's own secret
// store, which is what delivers it to the pod.
type machineIdentityResponse struct {
	EnvironmentID   string `json:"environmentId"`
	EnvironmentName string `json:"environmentName"`
	// Issuer is the registered issuer a token from this identity carries;
	// Subject is what such a token resolves by, and UserID the erun user row
	// those two are mapped to.
	Issuer          string `json:"issuer"`
	Subject         string `json:"subject"`
	ClientID        string `json:"clientId"`
	ClientSecret    string `json:"clientSecret"`
	UserID          string `json:"userId"`
	AlreadyEnrolled bool   `json:"alreadyEnrolled"`
}

// RegisterMachineIdentityRoutes always registers the route, even with no
// provisioner configured: an unconfigured dependency is answered with an
// actionable 501 rather than by making the route vanish into a 404, the same
// rule the cloud-provider-alias storage routes follow. A caller told the route
// does not exist cannot tell a deployment gap from a typo.
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
	// A second provisioning of one environment is the identity that already
	// exists, not a new one — reported as 200 so a caller looping over
	// environments can tell the two apart without diffing credentials.
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
