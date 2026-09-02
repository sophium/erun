package routes

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// EnvironmentHostnameWriter performs the platform's own wildcard A-record
// write for an environment's own subzone -- the PowerDNS access `erun
// expose` cannot assume every caller has: the Ingress it
// applies lands in the target environment's own cluster, which the caller
// already has credentials for, but the DNS write lands in the platform's
// cluster, which a developer's local environment never does. Satisfied by
// *dns01broker.PowerDNSWriter.
type EnvironmentHostnameWriter interface {
	UpsertA(fqdn, value string) error
	DeleteA(fqdn string) error
}

// EnvironmentHostnameRoutes lets a tenant point its own environment's
// wildcard hostname at an IP through the platform API, for a caller with no
// direct PowerDNS access to the platform's cluster. Authorized the same way
// POST .../dns01-token already is: TenantUserClass, enforced implicitly by
// r.environments.Get's row-level security -- reaching this handler at all
// already proves membership in the environment's own tenant, and the
// wildcard name is always derived from that environment, never from
// caller-supplied input, so a tenant can never write outside its own
// subzone.
type EnvironmentHostnameRoutes struct {
	environments EnvironmentRepository
	tenants      ConfigTenantRepository
	// writer is nil when the platform's PowerDNS write path is not
	// configured; the handlers then report 501 rather than claiming a write
	// they cannot perform.
	writer EnvironmentHostnameWriter
	// servicesZone is the zone tenant hostnames live under -- the same zone
	// the DNS-01 broker's own PowerDNSWriter is configured against.
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

// setHostname upserts the caller's own environment's wildcard A record. A
// private or loopback target (e.g. 127.0.0.1, for a local cluster) is
// accepted on purpose, not refused as a mistake -- the whole point of this
// route is letting a developer's local preview resolve.
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

// deleteHostname removes the caller's own environment's wildcard A record,
// symmetric with `erun unexpose`'s direct pdnsutil path.
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

// resolveWildcard resolves the caller's own environment's wildcard hostname
// -- *.<tenant>-<env>.<servicesZone>, the same formula `erun expose` computes
// locally via pdnsutil. environments.Get is row-level-security scoped to the
// caller's tenant, so this can only ever resolve to the caller's own
// environment.
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
	return fmt.Sprintf("*.%s.%s", eruncommon.KubernetesNamespaceName(tenant.Name, environment.Name), r.servicesZone), nil
}
