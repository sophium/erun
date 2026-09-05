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

// NamespaceDeleteRefusedError names a namespace delete the cluster never
// accepted -- RBAC, an admission webhook, an API server that answered the
// request with anything but "accepted". It is a distinct type from
// NamespaceTerminationBlockedError because the two are different situations
// with different remedies, and reporting the first as the second is
// confidently wrong twice over: no wait happened, and the namespace's own
// termination conditions describe a teardown that never started, sending an
// operator to investigate finalizers when the fault is a missing grant.
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
	traceRetractACMEChallenges(ctx, contextName, namespace)

	args = append(args, "delete", "namespace", namespace, "--ignore-not-found", "--timeout", NamespaceDeleteTimeout.String())
	ctx.TraceCommand("", "kubectl", args...)
}

// traceRetractACMEChallenges mirrors retractACMEChallenges for a dry run, so
// the traced sequence is the one a real delete runs.
func traceRetractACMEChallenges(ctx Context, contextName, namespace string) {
	ctx.TraceCommand("", "kubectl", append(kubernetesContextArgs(contextName),
		"-n", namespace, "delete", "certificates.cert-manager.io", "--all",
		"--ignore-not-found", "--timeout", acmeRetractTimeout.String())...)
	ctx.TraceCommand("", "kubectl", append(kubernetesContextArgs(contextName),
		"-n", namespace, "get", "challenges.acme.cert-manager.io", "-o", "name")...)
}

// DeleteKubernetesNamespace deletes a namespace and waits up to
// NamespaceDeleteTimeout for Kubernetes to finish tearing it down. A
// namespace still present after the timeout returns
// *NamespaceTerminationBlockedError naming its own conditions, so a caller can
// tell "still terminating, here is why" apart from every other kubectl
// failure; a delete the cluster never accepted returns
// *NamespaceDeleteRefusedError carrying kubectl's refusal, because that one is
// not a termination problem at all. A namespace that has actually disappeared
// by the time the timeout is checked (a benign race between the wait and the
// finalizer clearing) still reports success.
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
	// Retract any in-flight ACME challenge before the namespace teardown that
	// would remove the credential its cleanup needs (#1174). Deleting the
	// namespace first removes the env's DNS-01 token Secret as ordinary
	// content, and Kubernetes gives no ordering guarantee against cert-manager
	// finalizing a still-pending Challenge in the same namespace -- so the
	// finalizer would never clear and the namespace would sit in Terminating
	// for the full NamespaceDeleteTimeout. Best-effort by design: a cluster
	// with no cert-manager, or a challenge that will not finalize, must not
	// block the delete. If a challenge does survive this, the namespace's own
	// conditions still name it in NamespaceTerminationBlockedError below.
	retractionNote := retractACMEChallenges(contextName, namespace)

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
		if namespaceDeleteWasRefused(message) {
			// Neither the namespace's conditions nor the retraction note belong
			// here: both describe a teardown in progress, and this one never
			// started. kubectl's own refusal is the only fact that explains it.
			return &NamespaceDeleteRefusedError{
				Namespace: namespace,
				Detail:    namespaceDeleteRefusalDetail(message),
			}
		}
		return &NamespaceTerminationBlockedError{
			Namespace: namespace,
			// The retraction note rides along here rather than being logged: if
			// the namespace wedged AND the retraction could not run, those two
			// facts belong together, in the one string an operator reads off
			// the environment row.
			Detail: joinTerminationDetail(namespaceTerminationConditions(contextName, namespace), retractionNote),
		}
	}
	if message == "" {
		return fmt.Errorf("failed to delete kubernetes namespace %q in context %q: %w", namespace, contextName, err)
	}
	return fmt.Errorf("failed to delete kubernetes namespace %q in context %q: %w: %s", namespace, contextName, err, message)
}

// namespaceDeleteWasRefused reports whether kubectl's failure means the delete
// was never accepted, as opposed to accepted and then not finishing. The wait
// expiring is the one failure that genuinely is a termination problem, and
// kubectl names it; everything else it reports ended the request before the
// namespace could enter Terminating. An empty message says nothing either way
// and keeps the termination reading, where the namespace's own conditions are
// the only evidence left to go on.
func namespaceDeleteWasRefused(message string) bool {
	if strings.TrimSpace(message) == "" {
		return false
	}
	lowered := strings.ToLower(message)
	return !strings.Contains(lowered, "timed out") && !strings.Contains(lowered, "deadline exceeded")
}

// namespaceDeleteRefusalDetail carries kubectl's own refusal and, when that
// refusal is a permission problem, names the grant that resolves it. An
// operator handed only "forbidden" still has to work out that the fix is an
// RBAC rule -- and that the finalizers a termination message would have sent
// them to are not involved at all.
func namespaceDeleteRefusalDetail(message string) string {
	if !kubernetesRequestForbidden(message) {
		return message
	}
	return message + "\nthis kubeconfig's user is not permitted to delete this namespace; grant it delete on namespaces (RBAC) and retry. The namespace is not terminating, so its finalizers are not the cause."
}

// kubernetesRequestForbidden matches kubectl's own wording for a request the
// cluster refused on credentials or permissions, so a refusal can name its
// remedy instead of only quoting the server.
func kubernetesRequestForbidden(message string) bool {
	lowered := strings.ToLower(message)
	return strings.Contains(lowered, "forbidden") || strings.Contains(lowered, "unauthorized")
}

// acmeRetractTimeout bounds the pre-teardown challenge retraction. Short on
// purpose: it is an optimization that avoids a 20-minute wedge, so it must
// never itself become a long wait. A challenge that has not finalized by then
// is left to the namespace delete, where the webhook shim's own tolerance for
// an already-deleted token Secret clears the finalizer instead.
const acmeRetractTimeout = 90 * time.Second

// retractACMEChallenges deletes the namespace's cert-manager Certificates and
// waits for its ACME Challenges to finalize, so a challenge's cleanup runs
// while the DNS-01 token Secret it authenticates with still exists.
//
// Returns a note describing why the retraction could not run, or "" when it ran
// (or was legitimately unnecessary). The note is folded into the blocked-delete
// error if the namespace then wedges, which is the one place an operator is
// already looking. It is deliberately not logged: this is a library used by the
// CLI, and writing to the CLI's own output would be both noise and a golden
// change for every delete.
//
// The distinction matters. A cluster with no cert-manager has no CRDs, the read
// fails with "the server doesn't have a resource type", and skipping is exactly
// right at zero cost. A cluster that HAS cert-manager but refuses the read is
// the opposite: the retraction cannot work, the namespace will wedge, and
// treating that as "nothing to retract" is how this shipped inert the first
// time (#1183) -- the read was Forbidden, that was read as "no challenges", and
// the whole step was skipped with nothing to say so.
func retractACMEChallenges(contextName, namespace string) string {
	present, err := acmeChallengesPresent(contextName, namespace)
	if err != nil {
		if acmeCRDsAbsent(err.Error()) {
			// No cert-manager on this cluster: nothing to retract, and nothing
			// to report. The normal case for a local or test cluster.
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
	if output, deleteErr := Command("kubectl", args...).CombinedOutput(); deleteErr != nil {
		return fmt.Sprintf("deleting this namespace's cert-manager Certificates before teardown did not succeed (%s: %s); a challenge still finalizing will hold the namespace until the delete times out", deleteErr, strings.TrimSpace(string(output)))
	}

	// Wait for the challenges to actually go, rather than assuming the
	// Certificate delete cascaded synchronously -- cert-manager removes the
	// Order and its Challenges asynchronously, and it is the Challenge's
	// finalizer, not the Certificate's, that blocks the namespace.
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

// acmeCRDsAbsent reports whether a kubectl failure means cert-manager's ACME
// CRDs are not installed, as opposed to any other failure. Matched on kubectl's
// own wording for an unknown resource type; anything else -- Forbidden above
// all -- is a real problem and must not be mistaken for an absent CRD.
func acmeCRDsAbsent(message string) bool {
	lowered := strings.ToLower(message)
	return strings.Contains(lowered, "the server doesn't have a resource type") ||
		strings.Contains(lowered, "server could not find the requested resource") ||
		strings.Contains(lowered, "no matches for kind")
}

// acmeChallengesPresent reports whether the namespace currently has any ACME
// Challenge. The error is returned rather than folded into false, so the caller
// can tell "cert-manager is not installed" from "the read was refused".
func acmeChallengesPresent(contextName, namespace string) (bool, error) {
	args := append(kubernetesContextArgs(contextName),
		"-n", namespace, "get", "challenges.acme.cert-manager.io", "-o", "name")
	output, err := Command("kubectl", args...).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)) != "", nil
}

// joinTerminationDetail combines the namespace's own conditions with a note
// about the pre-teardown retraction, keeping either alone readable when the
// other is empty.
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

// kubernetesNamespaceExists dispatches to the subprocess or library path per
// the kubectl-namespace-get execution mode (see execution_mode.go).
func kubernetesNamespaceExists(contextName, namespace string) (bool, error) {
	if currentExecutionMode(kubectlNamespaceGetExecutionOperation) == ExecutionModeLibrary {
		return libraryKubernetesNamespaceExists(contextName, namespace)
	}
	return defaultKubernetesNamespaceExists(contextName, namespace)
}

func defaultKubernetesNamespaceExists(contextName, namespace string) (bool, error) {
	output, err := Command("kubectl", kubectlGetNamespaceArgs(contextName, namespace)...).CombinedOutput()
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

// KubernetesResourceNotFound matches kubectl's absent-resource message for any
// kind, so a caller can tell "not there" apart from "could not ask". Exported
// so callers outside this package (erun-ui's out-of-pod health probes) can
// make the same distinction instead of collapsing every kubectl failure to a
// plain negative.
func KubernetesResourceNotFound(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(message, "notfound") || strings.Contains(message, "not found")
}

func kubernetesNamespaceAlreadyExists(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(message, "alreadyexists") || strings.Contains(message, "already exists")
}
