// UIEnvironmentNodeSnapshot mirrors the Go uiEnvironmentNodeSnapshot: the cloud
// node an environment's cluster runs on, as the cloud-context poller last
// observed it (erun-ui/cloud_context_cache.go).
//
// It is a fact about the NODE, deliberately not folded into the environment's
// own status fields: a stopped node and a stopped environment are different
// things with different remedies, and a row that could conflate them would lose
// exactly the distinction this snapshot exists to carry.
export interface UIEnvironmentNodeSnapshot {
  name: string;
  // label is the operator-facing name for the same node; falls back to name.
  label?: string;
  // status is the poller's cached reading: 'running', 'pending', 'stopped',
  // 'unknown' once a known-good reading has gone stale, or '' when the poller
  // has not observed this node yet. Normalize it through cloudNodeState rather
  // than comparing strings — the last two both mean "we do not know", and
  // neither may render as stopped.
  status: string;
}
