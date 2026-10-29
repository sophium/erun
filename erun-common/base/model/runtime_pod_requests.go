package model

// Requests come from the live pod spec, not the cgroup reading: no cgroup file
// carries a request, and the spec reflects LimitRange and out-of-band defaults.

// KubernetesRequests is a resources.requests pair in millicores and bytes. Zero
// means undeclared and must not render as a measured "0".
type KubernetesRequests struct {
	CPUMilli    int64 `json:"cpuMilli,omitempty"`
	MemoryBytes int64 `json:"memoryBytes,omitempty"`
}

// IsZero reports whether nothing is declared, so callers can omit the pair.
func (r KubernetesRequests) IsZero() bool {
	return r.CPUMilli == 0 && r.MemoryBytes == 0
}

// RuntimeUsageRequests is the runtime pod's scheduler requests from the live spec.
type RuntimeUsageRequests struct {
	// Containers is each container's declared request by name.
	Containers map[string]KubernetesRequests `json:"containers,omitempty"`
	// Pod is max(max over init containers, sum over containers), as the scheduler admits it.
	Pod KubernetesRequests `json:"pod,omitempty"`
	// Unavailable says why the spec could not be read, so it never renders as "reserves nothing".
	Unavailable string `json:"unavailable,omitempty"`
}
