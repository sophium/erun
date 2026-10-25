package observe

import (
	"fmt"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/orchestration/clusterread"
)

// RunObservation reads one namespace's state, walking a non-Ready Certificate's chain to its
// failure reason. Read-only: every kubectl call is a literal `get`, the helm call a literal `status`.
func RunObservation(ctx core.Context, req config.ShellLaunchParams, params model.ObserveParams) (model.ObserveResult, error) {
	result := model.ObserveResult{Tenant: req.Tenant, Environment: req.Environment, Namespace: req.Namespace}

	podArgs := clusterread.ObserveGetArgs(req, "pods")
	quotaArgs := clusterread.ObserveGetArgs(req, "resourcequota")
	limitArgs := clusterread.ObserveGetArgs(req, "limitrange")
	ingressArgs := clusterread.ObserveGetArgs(req, "ingress")
	serviceArgs := clusterread.ObserveGetArgs(req, "service")
	certArgs := clusterread.ObserveGetArgs(req, "certificates.cert-manager.io")
	releaseName := runtimetarget.RuntimeReleaseName(req.Tenant)
	helmArgs := clusterread.ObserveHelmStatusArgs(req)
	helmListArgs := clusterread.ObserveHelmListArgs(req, releaseName)

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
		secretArgs[i] = clusterread.ObserveGetArgs(req, "secret", check.Name)
		ctx.TraceCommand("", "kubectl", secretArgs[i]...)
	}

	if ctx.DryRun {
		return result, nil
	}

	var err error
	if result.Pods, err = fetchObservedPods(podArgs); err != nil {
		return model.ObserveResult{}, err
	}
	if result.ResourceQuotas, err = fetchObservedResourceQuotas(quotaArgs); err != nil {
		return model.ObserveResult{}, err
	}
	if result.LimitRanges, err = fetchObservedLimitRanges(limitArgs); err != nil {
		return model.ObserveResult{}, err
	}
	// These fetchers are shared, so the "observe:" prefix is added here.
	if result.Ingresses, err = clusterread.FetchObservedIngresses(ingressArgs); err != nil {
		return model.ObserveResult{}, fmt.Errorf("observe: %w", err)
	}
	if result.Services, err = clusterread.FetchObservedServices(serviceArgs); err != nil {
		return model.ObserveResult{}, fmt.Errorf("observe: %w", err)
	}
	if result.Certificates, err = clusterread.FetchObservedCertificates(ctx, req, certArgs); err != nil {
		return model.ObserveResult{}, err
	}
	for i, check := range params.Secrets {
		result.Secrets = append(result.Secrets, fetchObservedSecretCheck(secretArgs[i], check))
	}

	result.HelmRelease = clusterread.FetchObservedHelmRelease(helmArgs, helmListArgs, releaseName, req.Namespace)
	result.Drift = computeObserveDrift(req, result.HelmRelease, result.Pods)

	return result, nil
}
