package model

// A resize moves the runtime and dind containers' limits, never the scheduler request, and rolls
// the pod once through the normal deploy path. Since it restarts the pod, any held activity
// lease refuses it unless overridden.

// RuntimeResizeInput is explicit values merged onto the current size, or the standing
// recommendation; the dind sidecar has no recommendation path.
type RuntimeResizeInput struct {
	CPU                 string
	Memory              string
	DindCPU             string
	DindMemory          string
	ApplyRecommendation bool
}

// RuntimeResizeAction is one resource's resolved change.
type RuntimeResizeAction struct {
	Resource string `json:"resource"`
	From     string `json:"from"`
	To       string `json:"to"`
}

// RuntimeResizePlan is the resolved outcome of a resize; NoOp means already sized.
type RuntimeResizePlan struct {
	Tenant      string
	Environment string
	Current     RuntimePodResources
	Target      RuntimePodResources
	DindCurrent RuntimePodResources
	DindTarget  RuntimePodResources
	Actions     []RuntimeResizeAction
	NoOp        bool
}

// RuntimeResizeParams is the resize input shared by both transports.
type RuntimeResizeParams struct {
	Tenant        string
	Environment   string
	Input         RuntimeResizeInput
	OverrideLease bool
	Holder        EnvironmentActivityLeaseHolder
}

// RuntimeResizeResult reports what a resize did or would do.
type RuntimeResizeResult struct {
	Plan             RuntimeResizePlan
	OverriddenLeases []EnvironmentActivityLease
}
