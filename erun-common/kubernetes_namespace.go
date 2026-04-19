package eruncommon

import (
	"fmt"
	"os/exec"
	"strings"
)

func TraceEnsureKubernetesNamespace(ctx Context, contextName, namespace string) {
	contextName = strings.TrimSpace(contextName)
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return
	}

	if contextName != "" {
		ctx.TraceCommand("", "kubectl", "--context", contextName, "get", "namespace", namespace, "-o", "name")
		ctx.TraceCommand("", "kubectl", "--context", contextName, "create", "namespace", namespace)
		return
	}

	ctx.TraceCommand("", "kubectl", "get", "namespace", namespace, "-o", "name")
	ctx.TraceCommand("", "kubectl", "create", "namespace", namespace)
}

func EnsureKubernetesNamespace(contextName, namespace string) error {
	if exists, err := kubernetesNamespaceExists(contextName, namespace); err != nil {
		return err
	} else if exists {
		return nil
	}

	args := []string{}
	if strings.TrimSpace(contextName) != "" {
		args = append(args, "--context", contextName)
	}
	args = append(args, "create", "namespace", namespace)

	output, err := exec.Command("kubectl", args...).CombinedOutput()
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
		return deploy(params)
	}
}

func kubernetesNamespaceExists(contextName, namespace string) (bool, error) {
	args := []string{}
	if strings.TrimSpace(contextName) != "" {
		args = append(args, "--context", contextName)
	}
	args = append(args, "get", "namespace", namespace, "-o", "name")

	output, err := exec.Command("kubectl", args...).CombinedOutput()
	if err == nil {
		return true, nil
	}

	message := strings.TrimSpace(string(output))
	if kubernetesNamespaceNotFound(message) {
		return false, nil
	}
	if message == "" {
		return false, fmt.Errorf("failed to check kubernetes namespace %q in context %q: %w", namespace, contextName, err)
	}
	return false, fmt.Errorf("failed to check kubernetes namespace %q in context %q: %w: %s", namespace, contextName, err, message)
}

func kubernetesNamespaceNotFound(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(message, "notfound") || strings.Contains(message, "not found")
}

func kubernetesNamespaceAlreadyExists(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(message, "alreadyexists") || strings.Contains(message, "already exists")
}
