package doctor

import (
	"fmt"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// reportHostCredentials reports whether the env's injected, temporary AWS credentials
// are still usable. Degrades on an unreachable pod.
func reportHostCredentials(ctx core.Context, store config.ConfigStore, result config.OpenResult, diagnosis deploy.DeployDiagnosisResult) error {
	if !result.EnvConfig.HasAWSCloudAlias() {
		return nil
	}
	if diagnosis.ClusterUnreachable {
		return reportPodSkippedUnreachable(ctx, "Host AWS credentials")
	}
	status, err := cloud.InspectHostAWSCredentials(ctx, nil, store, result)
	if err != nil {
		if ctx.DryRun {
			return nil
		}
		return reportPodUnreachable(ctx, "Host AWS credentials", err)
	}
	if ctx.DryRun {
		return nil
	}
	if _, err := fmt.Fprintf(ctx.Stdout, "== Host AWS credentials ==\nAlias:  %s\nRegion: %s\nStatus: %s\n\n",
		status.Alias, hostCredentialsRegionLine(status), hostCredentialsStatusLine(status, result)); err != nil {
		return err
	}
	return nil
}

func hostCredentialsRegionLine(status cloud.HostCredentialsStatus) string {
	if status.Region == "" {
		return "none resolved — AWS calls in this environment have no default region until the alias carries an SSO region or the environment names an ECR registry"
	}
	return status.Region
}

func hostCredentialsStatusLine(status cloud.HostCredentialsStatus, result config.OpenResult) string {
	refresh := fmt.Sprintf("run `erun cloud refresh %s %s`", result.Tenant, result.Environment)
	switch {
	case !status.Present:
		return fmt.Sprintf("absent — the %s profile is not in the runtime pod; %s", status.Profile, refresh)
	case status.Expired:
		return fmt.Sprintf("EXPIRED at %s — %s", status.Expiration.UTC().Format(time.RFC3339), refresh)
	case status.Expiration.IsZero():
		return fmt.Sprintf("present in the %s profile, expiry unrecorded — %s if AWS calls are failing", status.Profile, refresh)
	default:
		return fmt.Sprintf("valid until %s", status.Expiration.UTC().Format(time.RFC3339))
	}
}
