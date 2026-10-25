package kube

import (
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config/execmode"
	"github.com/sophium/erun/erun-common/base/core"
)

// NamespaceDeleteTimeout bounds the wait so a namespace stuck on a finalizer
// cannot block the caller indefinitely.
const NamespaceDeleteTimeout = 20 * time.Minute

// NamespaceTerminationBlockedError names a namespace still terminating after NamespaceDeleteTimeout, with its conditions.
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

// NamespaceDeleteRefusedError names a delete the cluster never accepted; unlike a
// blocked termination, no wait happened and finalizers are not involved.
type NamespaceDeleteRefusedError struct {
	Namespace string
	Detail    string
}

func (e *NamespaceDeleteRefusedError) Error() string {
	if e.Detail == "" {
		return fmt.Sprintf("namespace %q was not deleted: the cluster refused the request", e.Namespace)
	}
	return fmt.Sprintf("namespace %q was not deleted: the cluster refused the request:\n%s", e.Namespace, e.Detail)
}

// TraceEnsureKubernetesNamespace traces the namespace check and, when it will run, the create.
// A dry run cannot ask the cluster, so it traces the create as conditional.
func TraceEnsureKubernetesNamespace(ctx core.Context, contextName, namespace string) {
	contextName = strings.TrimSpace(contextName)
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return
	}

	ctx.TraceCommand("", "kubectl", kubectlGetNamespaceArgs(contextName, namespace)...)

	if ctx.DryRun {
		ctx.Trace("deploy: namespace " + namespace + " is created if the check above reports it missing")
		return
	}

	if exists, err := kubernetesNamespaceExists(contextName, namespace); err == nil && exists {
		return
	}
	ctx.TraceCommand("", "kubectl", append(kubernetesContextArgs(contextName), "create", "namespace", namespace)...)
}

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

	output, err := core.Command("kubectl", args...).CombinedOutput()
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

func TraceDeleteKubernetesNamespace(ctx core.Context, contextName, namespace string) {
	contextName = strings.TrimSpace(contextName)
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return
	}

	args := []string{}
	if contextName != "" {
		args = append(args, "--context", contextName)
	}
	traceRetractACMEChallenges(ctx, contextName, namespace)

	args = append(args, "delete", "namespace", namespace, "--ignore-not-found", "--timeout", NamespaceDeleteTimeout.String())
	ctx.TraceCommand("", "kubectl", args...)
}

// traceRetractACMEChallenges mirrors retractACMEChallenges for a dry run.
func traceRetractACMEChallenges(ctx core.Context, contextName, namespace string) {
	ctx.TraceCommand("", "kubectl", append(kubernetesContextArgs(contextName),
		"-n", namespace, "delete", "certificates.cert-manager.io", "--all",
		"--ignore-not-found", "--timeout", acmeRetractTimeout.String())...)
	ctx.TraceCommand("", "kubectl", append(kubernetesContextArgs(contextName),
		"-n", namespace, "get", "challenges.acme.cert-manager.io", "-o", "name")...)
}

// DeleteKubernetesNamespace deletes a namespace and waits up to NamespaceDeleteTimeout for it to go.
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
	// Retract ACME challenges first: deleting the namespace removes the DNS-01 token
	// Secret their finalizers need, wedging the namespace in Terminating. Best-effort.
	retractionNote := retractACMEChallenges(contextName, namespace)

	args = append(args, "delete", "namespace", namespace, "--ignore-not-found", "--timeout", NamespaceDeleteTimeout.String())

	output, err := core.Command("kubectl", args...).CombinedOutput()
	if err == nil {
		return nil
	}

	message := strings.TrimSpace(string(output))
	if exists, existErr := kubernetesNamespaceExists(contextName, namespace); existErr == nil {
		if !exists {
			return nil
		}
		if namespaceDeleteWasRefused(message) {
			// Termination conditions describe a teardown that never started here.
			return &NamespaceDeleteRefusedError{
				Namespace: namespace,
				Detail:    namespaceDeleteRefusalDetail(message),
			}
		}
		return &NamespaceTerminationBlockedError{
			Namespace: namespace,
			Detail:    joinTerminationDetail(namespaceTerminationConditions(contextName, namespace), retractionNote),
		}
	}
	if message == "" {
		return fmt.Errorf("failed to delete kubernetes namespace %q in context %q: %w", namespace, contextName, err)
	}
	return fmt.Errorf("failed to delete kubernetes namespace %q in context %q: %w: %s", namespace, contextName, err, message)
}

// namespaceDeleteWasRefused reports whether the delete was never accepted; only a
// wait timeout means it was accepted and did not finish.
func namespaceDeleteWasRefused(message string) bool {
	if strings.TrimSpace(message) == "" {
		return false
	}
	lowered := strings.ToLower(message)
	return !strings.Contains(lowered, "timed out") && !strings.Contains(lowered, "deadline exceeded")
}

// namespaceDeleteRefusalDetail carries kubectl's refusal and, for a permission problem, the grant that fixes it.
func namespaceDeleteRefusalDetail(message string) string {
	if !kubernetesRequestForbidden(message) {
		return message
	}
	return message + "\nthis kubeconfig's user is not permitted to delete this namespace; grant it delete on namespaces (RBAC) and retry. The namespace is not terminating, so its finalizers are not the cause."
}

func kubernetesRequestForbidden(message string) bool {
	lowered := strings.ToLower(message)
	return strings.Contains(lowered, "forbidden") || strings.Contains(lowered, "unauthorized")
}

// acmeRetractTimeout is short on purpose. A challenge still pending afterwards is cleared at
// namespace delete by the webhook's tolerance of an already-deleted token Secret.
const acmeRetractTimeout = 90 * time.Second

// retractACMEChallenges deletes Certificates and waits for Challenges to finalize while the
// DNS-01 token Secret still exists. It returns a note when the retraction could not run;
// it does not log, since CLI output would change for every delete. Absent CRDs skip
// silently, but a Forbidden read must not count as nothing to retract.
func retractACMEChallenges(contextName, namespace string) string {
	present, err := acmeChallengesPresent(contextName, namespace)
	if err != nil {
		if acmeCRDsAbsent(err.Error()) {
			return ""
		}
		return fmt.Sprintf("the pre-teardown ACME challenge retraction could not run (%s); if a challenge is still finalizing it will hold this namespace until the namespace delete times out", strings.TrimSpace(err.Error()))
	}
	if !present {
		return ""
	}

	args := append(kubernetesContextArgs(contextName),
		"-n", namespace, "delete", "certificates.cert-manager.io", "--all",
		"--ignore-not-found", "--timeout", acmeRetractTimeout.String())
	if output, deleteErr := core.Command("kubectl", args...).CombinedOutput(); deleteErr != nil {
		return fmt.Sprintf("deleting this namespace's cert-manager Certificates before teardown did not succeed (%s: %s); a challenge still finalizing will hold the namespace until the delete times out", deleteErr, strings.TrimSpace(string(output)))
	}

	// cert-manager removes Challenges asynchronously, and their finalizer is what blocks the namespace.
	deadline := time.Now().Add(acmeRetractTimeout)
	for time.Now().Before(deadline) {
		stillPresent, waitErr := acmeChallengesPresent(contextName, namespace)
		if waitErr != nil || !stillPresent {
			return ""
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Sprintf("this namespace's ACME challenges had not finalized %s after its Certificates were deleted; the namespace delete may wait on their finalizer", acmeRetractTimeout)
}

// acmeCRDsAbsent reports whether kubectl failed because the ACME CRDs are not installed.
func acmeCRDsAbsent(message string) bool {
	lowered := strings.ToLower(message)
	return strings.Contains(lowered, "the server doesn't have a resource type") ||
		strings.Contains(lowered, "server could not find the requested resource") ||
		strings.Contains(lowered, "no matches for kind")
}

func acmeChallengesPresent(contextName, namespace string) (bool, error) {
	args := append(kubernetesContextArgs(contextName),
		"-n", namespace, "get", "challenges.acme.cert-manager.io", "-o", "name")
	output, err := core.Command("kubectl", args...).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)) != "", nil
}

func joinTerminationDetail(conditions, note string) string {
	switch {
	case note == "":
		return conditions
	case conditions == "":
		return note
	default:
		return conditions + "\n" + note
	}
}

// namespaceTerminationConditions returns the namespace's status conditions, or "" on failure.
func namespaceTerminationConditions(contextName, namespace string) string {
	args := []string{}
	if contextName != "" {
		args = append(args, "--context", contextName)
	}
	args = append(args, "get", "namespace", namespace, "-o",
		`jsonpath={range .status.conditions[*]}{.type}={.status}{"\t"}{.message}{"\n"}{end}`)

	output, err := core.Command("kubectl", args...).CombinedOutput()
	if err != nil {
		return ""
	}
	return formatNamespaceConditions(string(output))
}

// formatNamespaceConditions renders conditions the way `kubectl describe namespace` does.
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

func kubernetesNamespaceExists(contextName, namespace string) (bool, error) {
	if execmode.CurrentExecutionMode(kubectlNamespaceGetExecutionOperation) == execmode.ExecutionModeLibrary {
		return libraryKubernetesNamespaceExists(contextName, namespace)
	}
	return defaultKubernetesNamespaceExists(contextName, namespace)
}

func defaultKubernetesNamespaceExists(contextName, namespace string) (bool, error) {
	output, err := core.Command("kubectl", kubectlGetNamespaceArgs(contextName, namespace)...).CombinedOutput()
	if err == nil {
		return true, nil
	}

	message := strings.TrimSpace(string(output))
	if KubernetesResourceNotFound(message) {
		return false, nil
	}
	if message == "" {
		return false, fmt.Errorf("failed to check kubernetes namespace %q in context %q: %w", namespace, contextName, err)
	}
	return false, fmt.Errorf("failed to check kubernetes namespace %q in context %q: %w: %s", namespace, contextName, err, message)
}

// KubernetesResourceNotFound reports whether kubectl's message means the resource is absent.
func KubernetesResourceNotFound(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(message, "notfound") || strings.Contains(message, "not found")
}

func kubernetesNamespaceAlreadyExists(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(message, "alreadyexists") || strings.Contains(message, "already exists")
}
