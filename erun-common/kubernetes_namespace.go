package eruncommon

import (
	"fmt"
	"strings"
	"time"
)

// NamespaceDeleteTimeout bounds how long DeleteKubernetesNamespace waits for
// Kubernetes to finish tearing a namespace down before giving up and naming
// why. Without a ceiling, a namespace stuck on an unsatisfiable finalizer (a
// DNS-01 solver that stopped answering, for example) blocks the caller for as
// long as Kubernetes is willing to sit in Terminating.
const NamespaceDeleteTimeout = 20 * time.Minute

// NamespaceTerminationBlockedError names a namespace that did not finish
// terminating within NamespaceDeleteTimeout, carrying its own conditions
// verbatim -- the same detail `kubectl describe namespace` shows, which turns
// a bare timeout into an actionable diagnosis.
type NamespaceTerminationBlockedError struct {
	Namespace string
	Detail    string
}

func (e *NamespaceTerminationBlockedError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("namespace %q did not finish terminating within %s", e.Namespace, NamespaceDeleteTimeout)
	}
	return fmt.Sprintf("namespace %q did not finish terminating within %s:\n%s", e.Namespace, NamespaceDeleteTimeout, e.Detail)
}

// TraceEnsureKubernetesNamespace traces the namespace check EnsureKubernetesNamespace
// performs and, only when the create that would follow is actually going to run,
// traces that too. A dry run cannot read the cluster to know which way the check
// resolves without every dry-run scenario in the suite needing a kubectl stub it
// otherwise has no reason to declare, so it states the create as conditional
// instead of asserting it: asserting it unconditionally is exactly the defect
// where a dry run showed a create the real run, finding the namespace
// already there, never performed. The real run does have a cluster to ask, so it
// resolves the same question live and traces only the branch that will execute —
// mirroring announceWorktreeVolumeChange's real/dry-run split for the same class
// of decision.
func TraceEnsureKubernetesNamespace(ctx Context, contextName, namespace string) {
	contextName = strings.TrimSpace(contextName)
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return
	}

	args := kubernetesContextArgs(contextName)
	ctx.TraceCommand("", "kubectl", append(append([]string{}, args...), "get", "namespace", namespace, "-o", "name")...)

	if ctx.DryRun {
		ctx.Trace("deploy: namespace " + namespace + " is created if the check above reports it missing")
		return
	}

	if exists, err := kubernetesNamespaceExists(contextName, namespace); err == nil && exists {
		return
	}
	ctx.TraceCommand("", "kubectl", append(append([]string{}, args...), "create", "namespace", namespace)...)
}

// kubernetesContextArgs is the shared --context prefix every kubectl invocation
// in this file carries when the caller names one.
func kubernetesContextArgs(contextName string) []string {
	contextName = strings.TrimSpace(contextName)
	if contextName == "" {
		return nil
	}
	return []string{"--context", contextName}
}

func EnsureKubernetesNamespace(contextName, namespace string) error {
	contextName = strings.TrimSpace(contextName)
	namespace = strings.TrimSpace(namespace)
	if exists, err := kubernetesNamespaceExists(contextName, namespace); err != nil {
		return err
	} else if exists {
		return nil
	}

	args := []string{}
	if contextName != "" {
		args = append(args, "--context", contextName)
	}
	args = append(args, "create", "namespace", namespace)

	output, err := Command("kubectl", args...).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if kubernetesNamespaceAlreadyExists(message) {
			return nil
		}
		if message == "" {
			return fmt.Errorf("failed to ensure kubernetes namespace %q in context %q: %w", namespace, contextName, err)
		}
		return fmt.Errorf("failed to ensure kubernetes namespace %q in context %q: %w: %s", namespace, contextName, err, message)
	}

	return nil
}

func TraceDeleteKubernetesNamespace(ctx Context, contextName, namespace string) {
	contextName = strings.TrimSpace(contextName)
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return
	}

	args := []string{}
	if contextName != "" {
		args = append(args, "--context", contextName)
	}
	args = append(args, "delete", "namespace", namespace, "--ignore-not-found", "--timeout", NamespaceDeleteTimeout.String())
	ctx.TraceCommand("", "kubectl", args...)
}

// DeleteKubernetesNamespace deletes a namespace and waits up to
// NamespaceDeleteTimeout for Kubernetes to finish tearing it down. A
// namespace still present after the timeout returns
// *NamespaceTerminationBlockedError naming its own conditions, so a caller can
// tell "still terminating, here is why" apart from every other kubectl
// failure. A namespace that has actually disappeared by the time the timeout
// is checked (a benign race between the wait and the finalizer clearing)
// still reports success.
func DeleteKubernetesNamespace(contextName, namespace string) error {
	contextName = strings.TrimSpace(contextName)
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return nil
	}

	args := []string{}
	if contextName != "" {
		args = append(args, "--context", contextName)
	}
	args = append(args, "delete", "namespace", namespace, "--ignore-not-found", "--timeout", NamespaceDeleteTimeout.String())

	output, err := Command("kubectl", args...).CombinedOutput()
	if err == nil {
		return nil
	}

	message := strings.TrimSpace(string(output))
	if exists, existErr := kubernetesNamespaceExists(contextName, namespace); existErr == nil {
		if !exists {
			return nil
		}
		return &NamespaceTerminationBlockedError{Namespace: namespace, Detail: namespaceTerminationConditions(contextName, namespace)}
	}
	if message == "" {
		return fmt.Errorf("failed to delete kubernetes namespace %q in context %q: %w", namespace, contextName, err)
	}
	return fmt.Errorf("failed to delete kubernetes namespace %q in context %q: %w: %s", namespace, contextName, err, message)
}

// namespaceTerminationConditions reads back a namespace's own status
// conditions, so a blocked delete can name its blocker instead of leaving the
// caller with only "timed out". "" (a read failure, or no conditions
// reported) leaves the caller with the bare timeout message.
func namespaceTerminationConditions(contextName, namespace string) string {
	args := []string{}
	if contextName != "" {
		args = append(args, "--context", contextName)
	}
	args = append(args, "get", "namespace", namespace, "-o",
		`jsonpath={range .status.conditions[*]}{.type}={.status}{"\t"}{.message}{"\n"}{end}`)

	output, err := Command("kubectl", args...).CombinedOutput()
	if err != nil {
		return ""
	}
	return formatNamespaceConditions(string(output))
}

// formatNamespaceConditions turns kubectl's raw "type=status<TAB>message"
// lines into the aligned "Type=Status  Message" shape `kubectl describe
// namespace` shows, so a blocked delete's own diagnosis reads the same way an
// operator would find it by hand.
func formatNamespaceConditions(raw string) string {
	type condition struct{ key, message string }
	var conditions []condition
	maxKey := 0
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		key := strings.TrimSpace(parts[0])
		message := ""
		if len(parts) > 1 {
			message = strings.TrimSpace(parts[1])
		}
		if len(key) > maxKey {
			maxKey = len(key)
		}
		conditions = append(conditions, condition{key: key, message: message})
	}

	lines := make([]string, 0, len(conditions))
	for _, c := range conditions {
		lines = append(lines, fmt.Sprintf("%-*s  %s", maxKey, c.key, c.message))
	}
	return strings.Join(lines, "\n")
}

func WrapHelmChartDeployerWithNamespaceEnsure(ensure NamespaceEnsurerFunc, deploy HelmChartDeployerFunc) HelmChartDeployerFunc {
	if deploy == nil {
		deploy = DeployHelmChart
	}
	if ensure == nil {
		return deploy
	}

	return func(params HelmDeployParams) error {
		if err := ensure(params.KubernetesContext, params.Namespace); err != nil {
			return err
		}
		if err := EnsureKubernetesResourceQuota(params.KubernetesContext, params.Namespace, params.NamespaceQuota); err != nil {
			return err
		}
		return deploy(params)
	}
}

func kubernetesNamespaceExists(contextName, namespace string) (bool, error) {
	args := []string{}
	if strings.TrimSpace(contextName) != "" {
		args = append(args, "--context", contextName)
	}
	args = append(args, "get", "namespace", namespace, "-o", "name")

	output, err := Command("kubectl", args...).CombinedOutput()
	if err == nil {
		return true, nil
	}

	message := strings.TrimSpace(string(output))
	if kubernetesResourceNotFound(message) {
		return false, nil
	}
	if message == "" {
		return false, fmt.Errorf("failed to check kubernetes namespace %q in context %q: %w", namespace, contextName, err)
	}
	return false, fmt.Errorf("failed to check kubernetes namespace %q in context %q: %w: %s", namespace, contextName, err, message)
}

// kubernetesResourceNotFound matches kubectl's absent-resource message for any
// kind, so a caller can tell "not there" apart from "could not ask".
func kubernetesResourceNotFound(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(message, "notfound") || strings.Contains(message, "not found")
}

func kubernetesNamespaceAlreadyExists(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(message, "alreadyexists") || strings.Contains(message, "already exists")
}
