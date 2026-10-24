package setup

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
)

// HostedRegistryProbeTimeout is short because the probe blocks an interactive
// choice.
const HostedRegistryProbeTimeout = 5 * time.Second

// HostedRegistryStatus reports hosted-registry availability. Reason and Recovery
// are set only for a cause the probe actually observed.
type HostedRegistryStatus struct {
	Host      string
	Available bool
	Reason    string
	Recovery  string
}

// Err returns nil for an available status.
func (s HostedRegistryStatus) Err() error {
	if s.Available {
		return nil
	}
	return fmt.Errorf("erun's hosted container registry %s %s. %s", s.Host, s.Reason, s.Recovery)
}

// ProbeHostedRegistry reports whether the hosted registry serves the OCI API.
// Any HTTP response, including 401, counts as available.
func ProbeHostedRegistry(ctx context.Context, client *http.Client) HostedRegistryStatus {
	return ProbeHostedRegistryAt(ctx, client, "https://"+config.HostedRegistryHost+"/v2/", config.HostedRegistryHost)
}

// ProbeHostedRegistryAt is ProbeHostedRegistry with the endpoint injected.
func ProbeHostedRegistryAt(ctx context.Context, client *http.Client, url, host string) HostedRegistryStatus {
	status := HostedRegistryStatus{Host: host}
	if client == nil {
		client = &http.Client{Timeout: HostedRegistryProbeTimeout}
	}
	ctx, cancel := context.WithTimeout(ctx, HostedRegistryProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		status.Reason = "could not be probed"
		status.Recovery = "Report this: building the probe request itself failed, which is a defect rather than a registry outage."
		return status
	}
	resp, err := client.Do(req)
	if err != nil {
		status.Reason, status.Recovery = describeHostedRegistryFailure(err)
		return status
	}
	defer func() { _ = resp.Body.Close() }()
	status.Available = true
	return status
}

func describeHostedRegistryFailure(err error) (reason, recovery string) {
	const chooseAnother = "Choose a different registry instead: --container-registry <host/namespace> for one you already have, or --cluster-registry for an in-cluster one."

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return "does not resolve",
			"The hosted registry is not deployed for this platform, so nothing would receive the images pushed to it. " + chooseAnother
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Sprintf("did not answer within %s", HostedRegistryProbeTimeout),
			"It may be starting or overloaded. Retry shortly, and if it stays unreachable, " + chooseAnother
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Sprintf("did not answer within %s", HostedRegistryProbeTimeout),
			"It may be starting or overloaded. Retry shortly, and if it stays unreachable, " + chooseAnother
	}
	return fmt.Sprintf("is not reachable (%v)", err),
		"Check network access to it from this machine. If it is genuinely down, " + chooseAnother
}
