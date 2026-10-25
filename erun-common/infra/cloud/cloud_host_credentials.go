package cloud

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/kube"
)

// HostAWSCredentialsProfile is the AWS_PROFILE the runtime chart wires into the pod.
const HostAWSCredentialsProfile = "erun-host"

// hostAWSCredentialsExpirationKey is ignored by AWS; erun reads it to report an expired session.
const hostAWSCredentialsExpirationKey = "x_erun_expiration"

// hostAWSCredentialsPath lives on the home PVC, so it outlives pod replacement.
const hostAWSCredentialsPath = "~/.aws/credentials"

// RefreshHostAWSCredentials writes short-lived credentials into the pod's erun-host profile.
// The secret goes over stdin, never into args, traces, or goldens.
func RefreshHostAWSCredentials(ctx core.Context, store config.CloudReadStore, result config.OpenResult, deps CloudDependencies) (model.HostCredentialsRefresh, error) {
	alias := strings.TrimSpace(result.EnvConfig.CloudProviderAlias)
	if alias == "" {
		return model.HostCredentialsRefresh{}, fmt.Errorf("environment %s/%s has no AWS cloud provider alias; attach one with `erun cloud set %s %s --alias <alias>`",
			result.Tenant, result.Environment, result.Tenant, result.Environment)
	}
	provider, err := ResolveCloudProvider(store, alias)
	if err != nil {
		return model.HostCredentialsRefresh{}, err
	}
	if provider.Provider != config.CloudProviderAWS {
		return model.HostCredentialsRefresh{}, fmt.Errorf("cloud provider alias %q is a %s alias; host credential refresh applies to AWS aliases only", provider.Alias, provider.Provider)
	}

	region := ResolveEnvironmentAWSRegion(store, result.EnvConfig)
	refresh := model.HostCredentialsRefresh{
		Alias:   provider.Alias,
		Profile: HostAWSCredentialsProfile,
		Path:    hostAWSCredentialsPath,
		Region:  region,
	}
	ctx.Trace(fmt.Sprintf("cloud refresh: alias=%s profile=%s region=%s", provider.Alias, HostAWSCredentialsProfile, regionTraceValue(region)))
	ctx.Trace("cloud refresh: credentials are exported from the host profile and streamed to the pod on stdin (never an argument or a trace line)")

	req := ShellLaunchParamsFromResult(result)
	if err := TraceAndWaitForRuntime(ctx, req); err != nil {
		return model.HostCredentialsRefresh{}, err
	}
	script := hostAWSCredentialsWriteScript()
	traceRuntimeExecCommand(ctx, hostAWSCredentialsWriteArgs(req, script), "host-credentials-write-script", script)
	if ctx.DryRun {
		return refresh, nil
	}

	credentials, err := ExportCloudProviderCredentials(ctx, store, alias, deps)
	if err != nil {
		return model.HostCredentialsRefresh{}, fmt.Errorf("export host credentials for %s: %w (run `erun cloud login --alias %s` if the SSO session lapsed)", provider.Alias, err, provider.Alias)
	}
	refresh.Expiration = credentials.Expiration
	if err := writeHostAWSCredentials(req, script, renderHostAWSCredentialsProfile(credentials, region)); err != nil {
		return model.HostCredentialsRefresh{}, err
	}
	return refresh, nil
}

// InspectHostAWSCredentials reports the pod profile's presence and expiry, never credential material.
func InspectHostAWSCredentials(ctx core.Context, runner seam.RemoteCommandRunnerFunc, store config.CloudReadStore, result config.OpenResult) (model.HostCredentialsStatus, error) {
	status := model.HostCredentialsStatus{
		Alias:   strings.TrimSpace(result.EnvConfig.CloudProviderAlias),
		Profile: HostAWSCredentialsProfile,
		Region:  ResolveEnvironmentAWSRegion(store, result.EnvConfig),
	}
	req := ShellLaunchParamsFromResult(result)
	out, err := kube.RunTracedRemoteCommand(ctx, runner, req, "host-credentials-read-script", hostAWSCredentialsReadScript())
	if err != nil {
		return model.HostCredentialsStatus{}, fmt.Errorf("read host credentials%s: %w", kube.FormatRemoteCommandStderr(out.Stderr), err)
	}
	if ctx.DryRun {
		return status, nil
	}
	present, expiration := parseHostAWSCredentialsReport(out.Stdout)
	status.Present = present
	status.Expiration = expiration
	status.Expired = present && !expiration.IsZero() && !expiration.After(time.Now())
	return status, nil
}

func regionTraceValue(region string) string {
	if strings.TrimSpace(region) == "" {
		return "<unresolved>"
	}
	return region
}

func traceRuntimeExecCommand(ctx core.Context, args []string, label, script string) {
	traceArgs := append([]string{}, args...)
	if len(traceArgs) > 0 {
		traceArgs[len(traceArgs)-1] = "<remote-script>"
	}
	ctx.TraceCommand("", "kubectl", traceArgs...)
	ctx.TraceBlock(label, script)
}

// hostAWSCredentialsWriteArgs uses -i (not -it) so stdin stays a pipe for the credential bytes.
func hostAWSCredentialsWriteArgs(req config.ShellLaunchParams, script string) []string {
	args := config.KubectlTargetArgs(req)
	args = append(args, "exec", "-i")
	return append(args, "deployment/"+config.RuntimeReleaseName(req.Tenant), "--", "/bin/sh", "-lc", script)
}

func writeHostAWSCredentials(req config.ShellLaunchParams, script, payload string) error {
	cmd := core.Command("kubectl", hostAWSCredentialsWriteArgs(req, script)...)
	cmd.Stdin = strings.NewReader(payload)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		if message := strings.TrimSpace(output.String()); message != "" {
			return fmt.Errorf("write host credentials into %s: %w: %s", config.RuntimeReleaseName(req.Tenant), err, message)
		}
		return fmt.Errorf("write host credentials into %s: %w", config.RuntimeReleaseName(req.Tenant), err)
	}
	return nil
}

// renderHostAWSCredentialsProfile includes the region for pods whose chart sets no AWS_REGION.
func renderHostAWSCredentialsProfile(credentials CloudProviderCredentials, region string) string {
	lines := []string{
		"[" + HostAWSCredentialsProfile + "]",
		"aws_access_key_id = " + strings.TrimSpace(credentials.AccessKeyID),
		"aws_secret_access_key = " + strings.TrimSpace(credentials.SecretAccessKey),
	}
	if token := strings.TrimSpace(credentials.SessionToken); token != "" {
		lines = append(lines, "aws_session_token = "+token)
	}
	if region = strings.TrimSpace(region); region != "" {
		lines = append(lines, "region = "+region)
	}
	if !credentials.Expiration.IsZero() {
		lines = append(lines, hostAWSCredentialsExpirationKey+" = "+credentials.Expiration.UTC().Format(time.RFC3339))
	}
	return strings.Join(lines, "\n") + "\n"
}

// hostAWSCredentialsWriteScript replaces only the erun-host section. The mktemp file
// and atomic mv keep concurrent refreshes from duplicating the profile.
func hostAWSCredentialsWriteScript() string {
	return strings.Join([]string{
		"set -eu",
		"umask 077",
		`dir="$HOME/.aws"`,
		`file="$dir/credentials"`,
		`mkdir -p "$dir"`,
		`tmp="$(mktemp "$dir/credentials.erun-refresh.XXXXXX")"`,
		`trap 'rm -f "$tmp"' EXIT`,
		`if [ -f "$file" ]; then`,
		`  awk -v profile='` + HostAWSCredentialsProfile + `' '`,
		`    /^[[:space:]]*\[/ { drop = ($0 ~ "^[[:space:]]*\\[" profile "\\][[:space:]]*$") }`,
		`    !drop { print }`,
		`  ' "$file" > "$tmp"`,
		"else",
		`  : > "$tmp"`,
		"fi",
		`if [ -s "$tmp" ]; then printf '\n' >> "$tmp"; fi`,
		`cat >> "$tmp"`,
		`chmod 600 "$tmp"`,
		`mv "$tmp" "$file"`,
	}, "\n")
}

// hostAWSCredentialsReadScript must never print a key, secret, or session token.
func hostAWSCredentialsReadScript() string {
	return strings.Join([]string{
		"set -eu",
		`file="$HOME/.aws/credentials"`,
		`if [ ! -f "$file" ]; then`,
		`  printf 'profile=absent\nexpiration=\n'`,
		"  exit 0",
		"fi",
		`awk -v profile='` + HostAWSCredentialsProfile + `' -v key='` + hostAWSCredentialsExpirationKey + `' '`,
		`  /^[[:space:]]*\[/ { inside = ($0 ~ "^[[:space:]]*\\[" profile "\\][[:space:]]*$"); if (inside) found = 1; next }`,
		`  inside && index($0, key) == 1 { eq = index($0, "="); if (eq > 0) { value = substr($0, eq + 1); gsub(/^[ \t]+|[ \t]+$/, "", value) } }`,
		`  END { printf "profile=%s\n", (found ? "present" : "absent"); printf "expiration=%s\n", value }`,
		`' "$file"`,
	}, "\n")
}

func parseHostAWSCredentialsReport(stdout string) (bool, time.Time) {
	var present bool
	var expiration time.Time
	for _, line := range strings.Split(stdout, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "profile":
			present = strings.TrimSpace(value) == "present"
		case "expiration":
			if parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value)); err == nil {
				expiration = parsed
			}
		}
	}
	return present, expiration
}

// ResolveEnvironmentAWSRegion returns "" when nothing resolves; callers must then omit
// AWS_REGION, since an empty value overrides the pod profile's region.
func ResolveEnvironmentAWSRegion(store config.CloudReadStore, env config.EnvConfig) string {
	if region := cloudContextRegionFromName(env.KubernetesContext); region != "" {
		return region
	}
	alias := strings.TrimSpace(env.CloudProviderAlias)
	if alias != "" {
		if provider, err := ResolveCloudProvider(store, alias); err == nil {
			if region := strings.TrimSpace(provider.SSORegion); region != "" {
				return region
			}
		}
	}
	return awsRegionFromContainerRegistries(env.ContainerRegistries)
}

func awsRegionFromContainerRegistries(registries config.ContainerRegistries) string {
	for _, registry := range registries.DistinctRegistries() {
		if region := awsRegionFromECRHost(registry); region != "" {
			return region
		}
	}
	return ""
}

func awsRegionFromECRHost(reference string) string {
	host := strings.TrimSpace(reference)
	if idx := strings.Index(host, "/"); idx >= 0 {
		host = host[:idx]
	}
	const marker = ".dkr.ecr."
	start := strings.Index(host, marker)
	if start < 0 {
		return ""
	}
	region, suffix, ok := strings.Cut(host[start+len(marker):], ".")
	if !ok || !strings.HasPrefix(suffix, "amazonaws.com") {
		return ""
	}
	return strings.TrimSpace(region)
}
