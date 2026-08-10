package eruncommon

// A port-forward carries two independent facts, and only one of them is what a
// caller means by "working": something holds the local port, and the tunnel
// behind it still reaches the far end. They come apart, and stay apart. A
// `kubectl port-forward` whose target pod is replaced keeps its listener bound
// indefinitely — it accepts every connection and answers none — so anything
// that decides on boundness alone calls a dead environment healthy, and nothing
// re-establishes what nothing noticed was broken.
//
// Naming the four combinations is what turns that into a diagnosable state
// rather than a timeout somebody eventually notices. It lives here because both
// transports make the same decision from the same two observations: the CLI
// when it decides whether to adopt an existing forward or replace it, the
// desktop when its activity sweep decides whether an environment is quiet or
// unreachable.

// PortForwardHealth is what one forward's two observations reduce to.
type PortForwardHealth string

const (
	// PortForwardUnestablished means nothing recorded a forward for this
	// environment — the ordinary state of an environment nobody opened.
	PortForwardUnestablished PortForwardHealth = "unestablished"
	// PortForwardDropped means a forward was established and its local port is
	// now free. Whatever held it exited; there is nothing to repair, only
	// something to start.
	PortForwardDropped PortForwardHealth = "dropped"
	// PortForwardStale means the local port is held and the far end is gone.
	// This is the one state that looks healthy to every check that stops at the
	// listener, and the only one that calls for re-establishing rather than
	// starting or leaving alone.
	PortForwardStale PortForwardHealth = "stale"
	// PortForwardServing means the edge answered, which is the only positive
	// evidence a forward works.
	PortForwardServing PortForwardHealth = "serving"
)

// ClassifyPortForward reduces the observations to the one state they describe.
// The arguments are deliberately separate booleans rather than one "reachable":
// collapsing them is the bug this exists to prevent.
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

// NeedsReestablishing reports whether the forward should be torn down and
// started again rather than reused or left alone. Only a stale forward
// qualifies: a dropped one has nothing to tear down, and a serving one is
// working, so restarting it would fight a healthy tunnel.
func (h PortForwardHealth) NeedsReestablishing() bool {
	return h == PortForwardStale
}

// Interrupted reports whether an environment that had a forward no longer has a
// working one. Dropped and stale are one answer to that question and differ
// only in whether there is a corpse to clear first — which is the acting
// caller's problem, not the diagnosis's.
//
// Saying them apart is what NeedsReestablishing is for. Saying them together is
// what this is for, because the distinction has no meaning to a client of the
// environment: both are unreachable, and neither recovers on its own. Dropped
// is also the ordinary one — any pod replacement makes kubectl exit outright,
// while the bound-but-dead shape is the rarer accident — so a response that
// acts on stale alone leaves the common case as the silent one.
func (h PortForwardHealth) Interrupted() bool {
	return h == PortForwardDropped || h == PortForwardStale
}
