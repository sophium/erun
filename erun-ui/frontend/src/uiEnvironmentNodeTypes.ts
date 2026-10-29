// A fact about the node, kept apart from env status: a stopped node and a
// stopped env have different remedies.
export interface UIEnvironmentNodeSnapshot {
  name: string;
  label?: string;
  // Normalize through cloudNodeState: 'unknown' and '' both mean "do not know",
  // never stopped.
  status: string;
}
