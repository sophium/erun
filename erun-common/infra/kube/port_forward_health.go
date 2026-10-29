package kube

// A forward's local port can stay bound after its pod is replaced, answering nothing,
// so health needs both boundness and an answering edge.

// PortForwardHealth classifies one forward.
type PortForwardHealth string

const (
	// PortForwardUnestablished means no forward was recorded.
	PortForwardUnestablished PortForwardHealth = "unestablished"
	// PortForwardDropped means the forward's local port is free again.
	PortForwardDropped PortForwardHealth = "dropped"
	// PortForwardStale means the local port is held but the far end is gone.
	PortForwardStale PortForwardHealth = "stale"
	// PortForwardServing means the edge answered.
	PortForwardServing PortForwardHealth = "serving"
)

// ClassifyPortForward keeps boundness and reachability separate on purpose.
func ClassifyPortForward(established, portIsBound, edgeAnswers bool) PortForwardHealth {
	switch {
	case !established:
		return PortForwardUnestablished
	case !portIsBound:
		return PortForwardDropped
	case !edgeAnswers:
		return PortForwardStale
	default:
		return PortForwardServing
	}
}

// NeedsReestablishing reports whether the forward must be torn down and restarted; only a stale one must.
func (h PortForwardHealth) NeedsReestablishing() bool {
	return h == PortForwardStale
}

// Interrupted reports whether a once-established forward is dropped or stale.
func (h PortForwardHealth) Interrupted() bool {
	return h == PortForwardDropped || h == PortForwardStale
}
