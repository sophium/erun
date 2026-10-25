package observe

import (
	"fmt"
	"strings"
)

func IsHelmReleaseNotFound(stderr string) bool {
	return strings.Contains(strings.ToLower(stderr), "release: not found")
}

// ObserveHelmReadErrorMessage classifies a failed helm read into an actionable message.
func ObserveHelmReadErrorMessage(releaseName, namespace, stderr string, err error) string {
	lower := strings.ToLower(stderr)
	switch {
	case strings.Contains(lower, "forbidden") || strings.Contains(lower, "unauthorized"):
		return fmt.Sprintf("observe: not authorized to read helm release %q in namespace %q (%s) — helm release state is stored as Secrets, so the caller's kubeconfig needs read access to Secrets in this namespace", releaseName, namespace, stderr)
	case strings.Contains(strings.ToLower(err.Error()), "executable file not found"):
		return "observe: helm is not installed or not on PATH — install helm to let observe read the runtime release"
	case stderr != "":
		return fmt.Sprintf("observe: could not read helm release %q in namespace %q: %s", releaseName, namespace, stderr)
	default:
		return fmt.Sprintf("observe: could not read helm release %q in namespace %q: %v", releaseName, namespace, err)
	}
}
