package eruncommon

// A usage reading and the sizing advice it implies are two answers to one
// question, and separating them is how an operator ends up holding an alarm
// with no remedy: the thresholds that warn and the evidence the recommender
// demanded were tuned apart, so an environment pinned at its memory ceiling
// produced "you are at 99% of your limit" and nothing else. RuntimeUsageReport
// is the pair, produced by one call from one body of evidence, so every
// transport that can show a warning shows the recommendation beside it.
//
// The pairing is deliberately not a second recommendation engine. The live
// reading is handed to RecommendRuntimeSizing as one more observation, merged
// with the retained history before a single verdict is computed; a transport
// that ran its own sizing pass over the same reading would be free to disagree
// with the one next to it.

// RuntimeUsageReport is an environment's live reading together with the
// standing sizing recommendation derived from that reading and its retained
// history. Embeds RuntimeUsage so every existing field stays where a consumer
// already reads it; Sizing is additive and matches what `erun list` reports
// under `runtime-pod:`.
type RuntimeUsageReport struct {
	RuntimeUsage
	// Sizing is nil only when there is nothing observed to reason from at all,
	// or when the history could not be read -- silence, never a guess.
	Sizing *RuntimeSizingRecommendation `json:"sizing,omitempty"`
}

// ResolveRuntimeUsageReport pairs a just-taken reading with the recommendation
// that answers it. A history read failure is silence rather than an error, the
// same posture EnvironmentRuntimeSizing takes: a report is advisory and must
// not fail over its own bookkeeping.
func ResolveRuntimeUsageReport(tenant string, env EnvConfig, usage RuntimeUsage) RuntimeUsageReport {
	report := RuntimeUsageReport{RuntimeUsage: usage}
	history, err := LoadRuntimeUsageHistory(tenant, env.Name)
	if err != nil {
		return report
	}
	recommendation, ok := RecommendRuntimeSizing(RuntimeSizingParams{
		History: history,
		Ceiling: env.NamespaceQuota,
		Live:    &usage,
	})
	if !ok {
		return report
	}
	report.Sizing = &recommendation
	return report
}
