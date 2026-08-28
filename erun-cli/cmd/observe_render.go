package cmd

import (
	"fmt"
	"sort"
	"strings"

	common "github.com/sophium/erun/erun-common"
)

func writeObserveResult(ctx common.Context, result common.ObserveResult) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "Namespace: %s\n", valueOrNone(result.Namespace)); err != nil {
		return err
	}
	if err := writeObservePods(ctx, result.Pods); err != nil {
		return err
	}
	if err := writeObserveResourceQuotas(ctx, result.ResourceQuotas); err != nil {
		return err
	}
	if err := writeObserveLimitRanges(ctx, result.LimitRanges); err != nil {
		return err
	}
	if err := writeObserveIngresses(ctx, result.Ingresses); err != nil {
		return err
	}
	if err := writeObserveCertificates(ctx, result.Certificates); err != nil {
		return err
	}
	if err := writeObserveSecrets(ctx, result.Secrets); err != nil {
		return err
	}
	if err := writeObserveHelmRelease(ctx, result.HelmRelease); err != nil {
		return err
	}
	return writeObserveDrift(ctx, result.Drift)
}

func writeObservePods(ctx common.Context, pods []common.ObservedPod) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "Pods (%d):\n", len(pods)); err != nil {
		return err
	}
	for _, pod := range pods {
		status := "ready"
		if !pod.Ready {
			status = "not ready"
			if pod.Reason != "" {
				status += ": " + pod.Reason
			}
		}
		if _, err := fmt.Fprintf(ctx.Stdout, "  %s: %s (phase %s, restarts %d)\n", pod.Name, status, pod.Phase, pod.RestartCount); err != nil {
			return err
		}
		for _, container := range pod.Containers {
			if _, err := fmt.Fprintf(ctx.Stdout, "    %s: image %s, limits %s\n", container.Name, valueOrNone(container.Image), formatResourceMap(container.ResourceLimits)); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeObserveResourceQuotas(ctx common.Context, quotas []common.ObservedResourceQuota) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "ResourceQuotas (%d):\n", len(quotas)); err != nil {
		return err
	}
	for _, quota := range quotas {
		if _, err := fmt.Fprintf(ctx.Stdout, "  %s: used %s, hard %s\n", quota.Name, formatResourceMap(quota.Used), formatResourceMap(quota.Hard)); err != nil {
			return err
		}
	}
	return nil
}

func writeObserveLimitRanges(ctx common.Context, limitRanges []common.ObservedLimitRange) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "LimitRanges (%d):\n", len(limitRanges)); err != nil {
		return err
	}
	for _, lr := range limitRanges {
		if _, err := fmt.Fprintf(ctx.Stdout, "  %s:\n", lr.Name); err != nil {
			return err
		}
		for _, limit := range lr.Limits {
			if _, err := fmt.Fprintf(ctx.Stdout, "    %s: default %s, defaultRequest %s, max %s, min %s\n",
				limit.Type, formatResourceMap(limit.Default), formatResourceMap(limit.DefaultRequest), formatResourceMap(limit.Max), formatResourceMap(limit.Min)); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeObserveIngresses(ctx common.Context, ingresses []common.ObservedIngress) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "Ingresses (%d):\n", len(ingresses)); err != nil {
		return err
	}
	for _, ingress := range ingresses {
		if _, err := fmt.Fprintf(ctx.Stdout, "  %s: hosts %s\n", ingress.Name, formatStringList(ingress.Hosts)); err != nil {
			return err
		}
		for _, tls := range ingress.TLS {
			if _, err := fmt.Fprintf(ctx.Stdout, "    tls %s -> secret %s\n", formatStringList(tls.Hosts), valueOrNone(tls.SecretName)); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeObserveCertificates(ctx common.Context, certificates []common.ObservedCertificate) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "Certificates (%d):\n", len(certificates)); err != nil {
		return err
	}
	for _, cert := range certificates {
		status := "Ready"
		if !cert.Ready {
			status = "not Ready"
			if cert.Reason != "" {
				status += ": " + cert.Reason
			}
			if cert.Message != "" {
				status += " (" + cert.Message + ")"
			}
		}
		if _, err := fmt.Fprintf(ctx.Stdout, "  %s: %s, secret %s, dnsNames %s\n", cert.Name, status, valueOrNone(cert.SecretName), formatStringList(cert.DNSNames)); err != nil {
			return err
		}
		if err := writeObserveCertificateOrders(ctx, cert.Orders); err != nil {
			return err
		}
	}
	return nil
}

func writeObserveCertificateOrders(ctx common.Context, orders []common.ObservedCertificateOrder) error {
	for _, order := range orders {
		if _, err := fmt.Fprintf(ctx.Stdout, "    order %s: state %s, reason %s\n", order.Name, valueOrNone(order.State), valueOrNone(order.Reason)); err != nil {
			return err
		}
		for _, challenge := range order.Challenges {
			if _, err := fmt.Fprintf(ctx.Stdout, "      challenge %s: type %s, dnsName %s, state %s, reason %s\n",
				challenge.Name, valueOrNone(challenge.Type), valueOrNone(challenge.DNSName), valueOrNone(challenge.State), valueOrNone(challenge.Reason)); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeObserveSecrets(ctx common.Context, secrets []common.ObservedSecretCheck) error {
	if len(secrets) == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(ctx.Stdout, "Secrets checked (%d):\n", len(secrets)); err != nil {
		return err
	}
	for _, secret := range secrets {
		line := fmt.Sprintf("  %s[%s]: exists=%t hasKey=%t", secret.Name, secret.Key, secret.Exists, secret.HasKey)
		if secret.Error != "" {
			line += " error=" + secret.Error
		}
		if _, err := fmt.Fprintln(ctx.Stdout, line); err != nil {
			return err
		}
	}
	return nil
}

func writeObserveHelmRelease(ctx common.Context, release *common.ObservedHelmRelease) error {
	if release == nil {
		return nil
	}
	if !release.Found {
		return writeObserveHelmReleaseNotFound(ctx, release)
	}
	if err := writeObserveHelmReleaseDetails(ctx, release); err != nil {
		return err
	}
	if release.Error != "" {
		_, err := fmt.Fprintf(ctx.Stdout, "  warning: %s\n", release.Error)
		return err
	}
	return nil
}

// writeObserveHelmReleaseNotFound distinguishes a confirmed absence (no
// Error) from a read that failed (Error set): a caller must never see the
// same "nothing here" line for both.
func writeObserveHelmReleaseNotFound(ctx common.Context, release *common.ObservedHelmRelease) error {
	if release.Error != "" {
		_, err := fmt.Fprintf(ctx.Stdout, "Helm release %s: could not read: %s\n", release.Name, release.Error)
		return err
	}
	_, err := fmt.Fprintf(ctx.Stdout, "Helm release %s: not found in namespace\n", release.Name)
	return err
}

func writeObserveHelmReleaseDetails(ctx common.Context, release *common.ObservedHelmRelease) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "Helm release %s: revision %d, status %s, chart %s-%s, appVersion %s\n",
		release.Name, release.Revision, valueOrNone(release.Status), valueOrNone(release.Chart), valueOrNone(release.ChartVersion), valueOrNone(release.AppVersion)); err != nil {
		return err
	}
	if len(release.ImageOverrides) > 0 {
		if _, err := fmt.Fprintf(ctx.Stdout, "  imageOverrides: %s\n", formatResourceMap(release.ImageOverrides)); err != nil {
			return err
		}
	}
	if release.RuntimePod != (common.RuntimePodResources{}) {
		if _, err := fmt.Fprintf(ctx.Stdout, "  runtime pod limits: cpu %s, memory %s\n", release.RuntimePod.CPU, release.RuntimePod.Memory); err != nil {
			return err
		}
	}
	return nil
}

func writeObserveDrift(ctx common.Context, drift []string) error {
	if len(drift) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "Drift: none detected")
		return err
	}
	if _, err := fmt.Fprintf(ctx.Stdout, "Drift (%d):\n", len(drift)); err != nil {
		return err
	}
	for _, finding := range drift {
		if _, err := fmt.Fprintf(ctx.Stdout, "  %s\n", finding); err != nil {
			return err
		}
	}
	return nil
}

func formatResourceMap(values map[string]string) string {
	if len(values) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(values))
	for key, value := range values {
		parts = append(parts, key+"="+value)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func formatStringList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}
