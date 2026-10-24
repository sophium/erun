package routes

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
)

// EnvironmentHostnameWriter writes an environment's wildcard A record in the
// platform's DNS, which a developer's local environment cannot reach directly.
type EnvironmentHostnameWriter interface {
	UpsertA(fqdn, value string) error
	DeleteA(fqdn string) error
}

// EnvironmentHostnameRoutes authorizes via environments.Get's RLS, and the name is
// always derived from that environment, so a tenant never writes outside its subzone.
type EnvironmentHostnameRoutes struct {
	environments EnvironmentRepository
	tenants      ConfigTenantRepository
	// writer is nil when PowerDNS writes are unconfigured; handlers then answer 501.
	writer       EnvironmentHostnameWriter
	servicesZone string
}

func RegisterEnvironmentHostnameRoutes(register ProtectedRouteRegistrar, environments EnvironmentRepository, tenants ConfigTenantRepository, writer EnvironmentHostnameWriter, servicesZone string) {
	routes := EnvironmentHostnameRoutes{environments: environments, tenants: tenants, writer: writer, servicesZone: strings.TrimSpace(servicesZone)}
	register(http.MethodPut, "/v1/environments/{environment_id}/hostname", http.HandlerFunc(routes.setHostname))
	register(http.MethodDelete, "/v1/environments/{environment_id}/hostname", http.HandlerFunc(routes.deleteHostname))
}

type setEnvironmentHostnameRequest struct {
	TargetIP string `json:"targetIp"`
}

type environmentHostnameResponse struct {
	Hostname string `json:"hostname"`
	TargetIP string `json:"targetIp,omitempty"`
}

// setHostname deliberately accepts private and loopback targets for local previews.
func (r EnvironmentHostnameRoutes) setHostname(w http.ResponseWriter, req *http.Request) {
	if r.writer == nil || r.servicesZone == "" {
		writeError(w, http.StatusNotImplemented, "environment hostname DNS write is not configured")
		return
	}
	var body setEnvironmentHostnameRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	targetIP := strings.TrimSpace(body.TargetIP)
	if net.ParseIP(targetIP) == nil {
		writeError(w, http.StatusBadRequest, "targetIp must be a valid IP address; a private or loopback address (e.g. 127.0.0.1) is allowed on purpose")
		return
	}
	wildcard, err := r.resolveWildcard(req)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	if err := r.writer.UpsertA(wildcard, targetIP); err != nil {
		writeInternalError(w, req, "dns write failed", err)
		return
	}
	writeJSON(w, http.StatusOK, environmentHostnameResponse{Hostname: wildcard, TargetIP: targetIP})
}

func (r EnvironmentHostnameRoutes) deleteHostname(w http.ResponseWriter, req *http.Request) {
	if r.writer == nil || r.servicesZone == "" {
		writeError(w, http.StatusNotImplemented, "environment hostname DNS write is not configured")
		return
	}
	wildcard, err := r.resolveWildcard(req)
	if err != nil {
		writeRepositoryError(w, req, err)
		return
	}
	if err := r.writer.DeleteA(wildcard); err != nil {
		writeInternalError(w, req, "dns delete failed", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// resolveWildcard returns *.<tenant>-<env>.<servicesZone>, matching `erun expose`.
func (r EnvironmentHostnameRoutes) resolveWildcard(req *http.Request) (string, error) {
	ctx := req.Context()
	environment, err := r.environments.Get(ctx, req.PathValue("environment_id"))
	if err != nil {
		return "", err
	}
	tenant, err := r.tenants.Current(ctx)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("*.%s.%s", config.KubernetesNamespaceName(tenant.Name, environment.Name), r.servicesZone), nil
}
