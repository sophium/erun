package eruncommon

import (
	"bytes"
	"fmt"
	"strings"
)

// ObserveParams configures which named Secrets to check for key presence.
// Pods, quotas, limit ranges, ingresses, and certificates are always reported
// in full: enumerating them costs nothing and reveals nothing sensitive.
type ObserveParams struct {
	Secrets []ObserveSecretCheck
}

// ObserveSecretCheck names one Secret/key pair to check for presence. The
// value itself is never read or reported, only whether the key exists.
type ObserveSecretCheck struct {
	Name string
	Key  string
}

// ObserveResult is an environment's observed Kubernetes state: a read-only
// snapshot an orchestrator can act on without composing kubectl or helm by
// hand.
type ObserveResult struct {
	Tenant         string                  `json:"tenant"`
	Environment    string                  `json:"environment"`
	Namespace      string                  `json:"namespace"`
	Pods           []ObservedPod           `json:"pods"`
	ResourceQuotas []ObservedResourceQuota `json:"resourceQuotas"`
	LimitRanges    []ObservedLimitRange    `json:"limitRanges"`
	Ingresses      []ObservedIngress       `json:"ingresses"`
	// Services is what the namespace actually runs, which is the list a
	// developer picks from when choosing something to expose.
	Services     []ObservedService     `json:"services"`
	Certificates []ObservedCertificate `json:"certificates"`
	Secrets      []ObservedSecretCheck `json:"secrets,omitempty"`
	// HelmRelease is the runtime release's own record of what should be
	// running. Nil in dry-run, where nothing was read yet.
	HelmRelease *ObservedHelmRelease `json:"helmRelease,omitempty"`
	// Drift names every disagreement found between the live release, the
	// running pods, and what the env config records — the pre-deploy
	// live-vs-plan diff an orchestrator must do before any env-shaping
	// deploy. Empty means nothing disagreed, not that nothing was checked.
	Drift []string `json:"drift,omitempty"`
}

// RunObservation reads pods, quota/limit usage, ingress routing, certificate
// readiness, and the runtime helm release for one namespace, walking a
// Certificate's CertificateRequest -> Order -> Challenge chain when it is not
// Ready so the caller gets the failure reason without composing that walk
// itself. It is read-only by construction: every kubectl call below is a
// literal `get` against a fixed resource kind, and the one helm call is a
// literal `status`, never a caller-supplied verb.
func RunObservation(ctx Context, req ShellLaunchParams, params ObserveParams) (ObserveResult, error) {
	result := ObserveResult{Tenant: req.Tenant, Environment: req.Environment, Namespace: req.Namespace}

	podArgs := observeGetArgs(req, "pods")
	quotaArgs := observeGetArgs(req, "resourcequota")
	limitArgs := observeGetArgs(req, "limitrange")
	ingressArgs := observeGetArgs(req, "ingress")
	serviceArgs := observeGetArgs(req, "service")
	certArgs := observeGetArgs(req, "certificates.cert-manager.io")
	releaseName := RuntimeReleaseName(req.Tenant)
	helmArgs := observeHelmStatusArgs(req)
	helmListArgs := observeHelmListArgs(req, releaseName)

	ctx.TraceCommand("", "kubectl", podArgs...)
	ctx.TraceCommand("", "kubectl", quotaArgs...)
	ctx.TraceCommand("", "kubectl", limitArgs...)
	ctx.TraceCommand("", "kubectl", ingressArgs...)
	ctx.TraceCommand("", "kubectl", serviceArgs...)
	ctx.TraceCommand("", "kubectl", certArgs...)
	ctx.TraceCommand("", "helm", helmArgs...)
	ctx.TraceCommand("", "helm", helmListArgs...)
	ctx.Trace("observe: when a certificate is not Ready, its CertificateRequest -> Order -> Challenge chain is read for the failure reason")

	secretArgs := make([][]string, len(params.Secrets))
	for i, check := range params.Secrets {
		secretArgs[i] = observeGetArgs(req, "secret", check.Name)
		ctx.TraceCommand("", "kubectl", secretArgs[i]...)
	}

	if ctx.DryRun {
		return result, nil
	}

	var err error
	if result.Pods, err = fetchObservedPods(podArgs); err != nil {
		return ObserveResult{}, err
	}
	if result.ResourceQuotas, err = fetchObservedResourceQuotas(quotaArgs); err != nil {
		return ObserveResult{}, err
	}
	if result.LimitRanges, err = fetchObservedLimitRanges(limitArgs); err != nil {
		return ObserveResult{}, err
	}
	if result.Ingresses, err = fetchObservedIngresses(ingressArgs); err != nil {
		return ObserveResult{}, err
	}
	if result.Services, err = fetchObservedServices(serviceArgs); err != nil {
		return ObserveResult{}, err
	}
	if result.Certificates, err = fetchObservedCertificates(ctx, req, certArgs); err != nil {
		return ObserveResult{}, err
	}
	for i, check := range params.Secrets {
		result.Secrets = append(result.Secrets, fetchObservedSecretCheck(secretArgs[i], check))
	}

	result.HelmRelease = fetchObservedHelmRelease(helmArgs, helmListArgs, releaseName, req.Namespace)
	result.Drift = computeObserveDrift(req, result.HelmRelease, result.Pods)

	return result, nil
}

// observeGetArgs builds a read-only `kubectl get <resource> [name] -o json`
// invocation. name is optional: omitted, it lists every object of that kind in
// the namespace.
func observeGetArgs(req ShellLaunchParams, resource string, name ...string) []string {
	args := kubectlTargetArgs(req)
	args = append(args, "get", resource)
	args = append(args, name...)
	return append(args, "-o", "json")
}

// runObserveKubectl runs a read-only kubectl get, returning stderr alongside
// any error so callers can distinguish "not found" and "unknown resource
// type" from a real failure without re-parsing a wrapped error string.
func runObserveKubectl(args []string) (stdout []byte, stderr string, err error) {
	cmd := Command("kubectl", args...)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	out, runErr := cmd.Output()
	return out, strings.TrimSpace(errBuf.String()), runErr
}

func kubectlErrorMessage(err error, stderr string) error {
	if stderr == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, stderr)
}

// isKubectlUnknownResource reports whether stderr is kubectl's "this API
// isn't installed" shape, so a cluster with no cert-manager CRDs reports zero
// certificates instead of failing the whole observation.
func isKubectlUnknownResource(stderr string) bool {
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "the server doesn't have a resource type") ||
		strings.Contains(lower, "no matches for kind") ||
		strings.Contains(lower, "could not find the requested resource")
}

func isKubectlNotFound(stderr string) bool {
	return strings.Contains(strings.ToLower(stderr), "notfound") ||
		strings.Contains(strings.ToLower(stderr), "not found")
}
