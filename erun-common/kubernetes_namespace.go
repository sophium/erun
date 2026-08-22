package eruncommon

import (
	"fmt"
	"strings"
)

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
	args = append(args, "delete", "namespace", namespace, "--ignore-not-found")
	ctx.TraceCommand("", "kubectl", args...)
}

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
	args = append(args, "delete", "namespace", namespace, "--ignore-not-found")

	output, err := Command("kubectl", args...).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return fmt.Errorf("failed to delete kubernetes namespace %q in context %q: %w", namespace, contextName, err)
		}
		return fmt.Errorf("failed to delete kubernetes namespace %q in context %q: %w: %s", namespace, contextName, err, message)
	}
	return nil
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
