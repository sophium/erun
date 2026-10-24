package observe

import (
	"fmt"

	"github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
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
	Tenant         string                       `json:"tenant"`
	Environment    string                       `json:"environment"`
	Namespace      string                       `json:"namespace"`
	Pods           []ObservedPod                `json:"pods"`
	ResourceQuotas []ObservedResourceQuota      `json:"resourceQuotas"`
	LimitRanges    []ObservedLimitRange         `json:"limitRanges"`
	Ingresses      []eruncommon.ObservedIngress `json:"ingresses"`
	// Services is what the namespace actually runs, which is the list a
	// developer picks from when choosing something to expose.
	Services     []eruncommon.ObservedService     `json:"services"`
	Certificates []eruncommon.ObservedCertificate `json:"certificates"`
	Secrets      []ObservedSecretCheck            `json:"secrets,omitempty"`
	// HelmRelease is the runtime release's own record of what should be
	// running. Nil in dry-run, where nothing was read yet.
	HelmRelease *eruncommon.ObservedHelmRelease `json:"helmRelease,omitempty"`
	// Drift names every disagreement found between the live release, the
	// running pods, and what the env config records — the pre-deploy
	// live-vs-plan diff an orchestrator must do before any env-shaping
	// deploy.
	//
	// The field is always present in a real run, which is why it is not
	// `omitempty`: an empty list is the verdict "the comparison ran and
	// nothing disagreed", and a consumer must be able to tell that apart from
	// "this key is never populated". A dry run reads nothing and so carries a
	// null here, the same "not determined" answer its sibling lists report —
	// never an empty list, which would assert a comparison that never ran.
	Drift []string `json:"drift"`
}

// RunObservation reads pods, quota/limit usage, ingress routing, certificate
// readiness, and the runtime helm release for one namespace, walking a
// Certificate's CertificateRequest -> Order -> Challenge chain when it is not
// Ready so the caller gets the failure reason without composing that walk
// itself. It is read-only by construction: every kubectl call below is a
// literal `get` against a fixed resource kind, and the one helm call is a
// literal `status`, never a caller-supplied verb.
func RunObservation(ctx core.Context, req config.ShellLaunchParams, params ObserveParams) (ObserveResult, error) {
	result := ObserveResult{Tenant: req.Tenant, Environment: req.Environment, Namespace: req.Namespace}

	podArgs := eruncommon.ObserveGetArgs(req, "pods")
	quotaArgs := eruncommon.ObserveGetArgs(req, "resourcequota")
	limitArgs := eruncommon.ObserveGetArgs(req, "limitrange")
	ingressArgs := eruncommon.ObserveGetArgs(req, "ingress")
	serviceArgs := eruncommon.ObserveGetArgs(req, "service")
	certArgs := eruncommon.ObserveGetArgs(req, "certificates.cert-manager.io")
	releaseName := config.RuntimeReleaseName(req.Tenant)
	helmArgs := eruncommon.ObserveHelmStatusArgs(req)
	helmListArgs := eruncommon.ObserveHelmListArgs(req, releaseName)

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
		secretArgs[i] = eruncommon.ObserveGetArgs(req, "secret", check.Name)
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
	// The read names itself and observe names the operation on top of it: these
	// two fetchers are shared with the services listing and expose's own reads,
	// so neither can carry "observe:" as its own prefix. The message is
	// unchanged -- "observe: " then "get ingress: ...", exactly as before.
	if result.Ingresses, err = eruncommon.FetchObservedIngresses(ingressArgs); err != nil {
		return ObserveResult{}, fmt.Errorf("observe: %w", err)
	}
	if result.Services, err = eruncommon.FetchObservedServices(serviceArgs); err != nil {
		return ObserveResult{}, fmt.Errorf("observe: %w", err)
	}
	if result.Certificates, err = eruncommon.FetchObservedCertificates(ctx, req, certArgs); err != nil {
		return ObserveResult{}, err
	}
	for i, check := range params.Secrets {
		result.Secrets = append(result.Secrets, fetchObservedSecretCheck(secretArgs[i], check))
	}

	result.HelmRelease = eruncommon.FetchObservedHelmRelease(helmArgs, helmListArgs, releaseName, req.Namespace)
	result.Drift = computeObserveDrift(req, result.HelmRelease, result.Pods)

	return result, nil
}
