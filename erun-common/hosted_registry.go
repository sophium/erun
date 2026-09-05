package eruncommon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// HostedRegistryProbeTimeout bounds the availability probe. It is short on
// purpose: the probe sits in front of an interactive choice (an `erun init`
// flag, a desktop toggle), so a hosted registry that is slow to answer must not
// hold the surface that is asking about it.
const HostedRegistryProbeTimeout = 5 * time.Second

// HostedRegistryStatus answers whether erun's hosted registry can be pushed to
// right now, and when it cannot, why and what resolves it. Reason and Recovery
// are set only for a cause the probe actually observed: an unreachable registry
// has several distinct causes with different remedies, and a confident remedy
// for a cause that was not checked is its own dead end (root AGENTS.md,
// "Smooth, Seamless, No Dead Ends").
type HostedRegistryStatus struct {
	Host      string
	Available bool
	Reason    string
	Recovery  string
}

// Err renders an unavailable status as an error that names the operation, the
// subject, and the way forward. Available statuses return nil.
func (s HostedRegistryStatus) Err() error {
	if s.Available {
		return nil
	}
	return fmt.Errorf("erun's hosted container registry %s %s. %s", s.Host, s.Reason, s.Recovery)
}

// ProbeHostedRegistry reports whether the hosted registry is serving the OCI
// distribution API.
//
// Any HTTP response counts as available, including 401. An unauthenticated GET
// /v2/ is exactly what a healthy token-authenticated registry rejects, so
// treating a non-2xx status as down would report every correctly configured
// registry as missing.
func ProbeHostedRegistry(ctx context.Context, client *http.Client) HostedRegistryStatus {
	return probeHostedRegistryAt(ctx, client, "https://"+HostedRegistryHost+"/v2/", HostedRegistryHost)
}

// probeHostedRegistryAt is ProbeHostedRegistry with the endpoint injected, so a
// test can exercise the status mapping — in particular that a 401 counts as
// available — against a real HTTP server instead of the live hostname.
func probeHostedRegistryAt(ctx context.Context, client *http.Client, url, host string) HostedRegistryStatus {
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

// describeHostedRegistryFailure maps a probe failure to the cause it actually
// observed. Each branch names a remedy that applies to that cause and no other.
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
