package cmd

import (
	"fmt"
	"time"

	common "github.com/sophium/erun/erun-common"
)

// reportHostCredentials tells the operator whether the environment's copy of
// their AWS identity is still usable. Injected host credentials are temporary,
// so without this the first sign of expiry is an unrelated failure deep in
// whatever tool reached for AWS first — a registry pull, usually. Runs only for
// an environment configured to act as the operator's identity; every other
// environment sees no change.
func reportHostCredentials(ctx common.Context, store common.ConfigStore, result common.OpenResult) error {
	if !result.EnvConfig.HasAWSCloudAlias() {
		return nil
	}
	status, err := common.InspectHostAWSCredentials(ctx, nil, store, result)
	if err != nil {
		return err
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

// hostCredentialsRegionLine names the missing-region failure mode from #904 in
// the same place: an alias with no resolvable region leaves the pod's AWS calls
// with no default region at all.
func hostCredentialsRegionLine(status common.HostCredentialsStatus) string {
	if status.Region == "" {
		return "none resolved — AWS calls in this environment have no default region until the alias carries an SSO region or the environment names an ECR registry"
	}
	return status.Region
}

func hostCredentialsStatusLine(status common.HostCredentialsStatus, result common.OpenResult) string {
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
