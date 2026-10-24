package config

// The runtime pod's declared resource *requests* are the scheduler's own input:
// a limit decides what a container may grow to, a request decides whether the
// pod is admitted at all and how much of the node it reserves while it runs.
// Every sizing surface erun had reported limits alone -- the cgroup ceiling a
// usage reading divides against -- and `Mem 0% of 27.0 GiB` reads as
// provisioned when the same environment reserves 1 GiB and holds 27 GiB only as
// a ceiling. No cgroup file carries a request (memory.max is the limit,
// memory.current the usage), so this cannot come from the reading script the
// rest of runtime_usage.go runs: it is read from the pod spec, the one place a
// request exists.
//
// It reads the live pod rather than the deploy's recorded intent because the
// pod spec is what the scheduler actually acted on -- the chart's own request
// defaults, a LimitRange's defaultRequest, and anything set out of band all end
// up there and only there. Like every other reading in this package it reports
// its own unavailability rather than failing the call: a pod spec that cannot
// be read costs the request figures alone, never the usage reading beside them.

// KubernetesRequests is one container's declared resources.requests, or a pod's
// effective request, in the units the scheduler sums them in: millicores of CPU
// and bytes of memory.
//
// A zero field is a container that declares nothing for that resource, which is
// the scheduler's own reading of it -- an undeclared request reserves nothing --
// so no renderer may turn it into a "0" that reads as a measured reservation.
type KubernetesRequests struct {
	CPUMilli    int64 `json:"cpuMilli,omitempty"`
	MemoryBytes int64 `json:"memoryBytes,omitempty"`
}

// IsZero reports whether this pair carries no declaration at all, so a caller
// can omit it instead of rendering a pair of zeros.
func (r KubernetesRequests) IsZero() bool {
	return r.CPUMilli == 0 && r.MemoryBytes == 0
}

// RuntimeUsageRequests is what the scheduler admits this environment's runtime
// pod on, read from the live pod spec.
type RuntimeUsageRequests struct {
	// Containers is each container's own declared request, keyed by container
	// name. A container that declares nothing for a resource is present with
	// that resource zero, matching the pod spec; the runtime container and the
	// erun-dind sidecar are the two an operator is comparing against their own
	// limits above.
	Containers map[string]KubernetesRequests `json:"containers,omitempty"`
	// Pod is the effective request the scheduler sizes the pod for:
	// max(max over the init containers, sum over the containers), per resource.
	// Kubernetes admits a pod on the larger of the init phase's peak and the
	// containers' sum, so summing the containers alone understates a pod whose
	// init container asks for more.
	Pod KubernetesRequests `json:"pod,omitempty"`
	// Unavailable names why the pod spec could not be read -- no pod for this
	// release, kubectl's own failure, a declared quantity this parser cannot
	// read. Set with the rest of the reading left empty, so "could not read the
	// reservation" never renders as "reserves nothing".
	Unavailable string `json:"unavailable,omitempty"`
}
