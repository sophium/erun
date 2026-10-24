package core

import (
	"os"
)

// InInjectedRuntimePod detects the chart-injected runtime pod.
func InInjectedRuntimePod() bool {
	_, _, ok := InjectedRuntimePodIdentity(os.Getenv)
	return ok
}
