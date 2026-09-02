package eruncommon

import (
	"bytes"
	"fmt"
	"strings"
	"time"
)

// HostAWSCredentialsProfile is the profile the runtime chart wires into the
// pod's AWS_PROFILE. Every writer and reader of the pod's copy of the operator's
// identity must agree on it, or the pod authenticates as nobody.
const HostAWSCredentialsProfile = "erun-host"

// hostAWSCredentialsExpirationKey records when the injected credentials lapse.
// AWS ignores the key; erun reads it back so an expired session is reported as
// such instead of surfacing later as an unrelated registry pull failure.
const hostAWSCredentialsExpirationKey = "x_erun_expiration"

// hostAWSCredentialsPath is the pod-side file, displayed to the operator. It
// lives on the home PVC and therefore outlives pod replacement.
const hostAWSCredentialsPath = "~/.aws/credentials"

// HostCredentialsRefresh reports what a refresh wrote, so a transport can tell
// the operator which profile now carries their identity and when it lapses.
type HostCredentialsRefresh struct {
	Alias      string    `json:"alias"`
	Profile    string    `json:"profile"`
	Path       string    `json:"path"`
	Region     string    `json:"region,omitempty"`
	Expiration time.Time `json:"expiration,omitempty"`
}

// HostCredentialsStatus is the pod's erun-host profile as read back from the
// environment. Region is the region the env resolves to; empty means none could
// be resolved, which leaves every AWS call in the pod without a default region.
type HostCredentialsStatus struct {
	Alias      string    `json:"alias"`
	Profile    string    `json:"profile"`
	Present    bool      `json:"present"`
	Expiration time.Time `json:"expiration,omitempty"`
	Expired    bool      `json:"expired"`
	Region     string    `json:"region,omitempty"`
}

// RefreshHostAWSCredentials mints short-lived credentials from the operator's
// own AWS profile and writes them into the env's runtime pod under the erun-host
// profile. The secret is read here and streamed to the pod on stdin, so it never
// appears in an argument, a trace line, or a golden.
func RefreshHostAWSCredentials(ctx Context, store CloudReadStore, result OpenResult, deps CloudDependencies) (HostCredentialsRefresh, error) {
	alias := strings.TrimSpace(result.EnvConfig.CloudProviderAlias)
	if alias == "" {
		return HostCredentialsRefresh{}, fmt.Errorf("environment %s/%s has no AWS cloud provider alias; attach one with `erun cloud set %s %s --alias <alias>`",
			result.Tenant, result.Environment, result.Tenant, result.Environment)
	}
	provider, err := ResolveCloudProvider(store, alias)
	if err != nil {
		return HostCredentialsRefresh{}, err
	}
	if provider.Provider != CloudProviderAWS {
		return HostCredentialsRefresh{}, fmt.Errorf("cloud provider alias %q is a %s alias; host credential refresh applies to AWS aliases only", provider.Alias, provider.Provider)
	}

	region := resolveEnvironmentAWSRegion(store, result.EnvConfig)
	refresh := HostCredentialsRefresh{
		Alias:   provider.Alias,
		Profile: HostAWSCredentialsProfile,
		Path:    hostAWSCredentialsPath,
		Region:  region,
	}
	ctx.Trace(fmt.Sprintf("cloud refresh: alias=%s profile=%s region=%s", provider.Alias, HostAWSCredentialsProfile, regionTraceValue(region)))
	ctx.Trace("cloud refresh: credentials are exported from the host profile and streamed to the pod on stdin (never an argument or a trace line)")

	req := ShellLaunchParamsFromResult(result)
	if err := traceAndWaitForRuntime(ctx, req); err != nil {
		return HostCredentialsRefresh{}, err
	}
	script := hostAWSCredentialsWriteScript()
	traceRuntimeExecCommand(ctx, hostAWSCredentialsWriteArgs(req, script), "host-credentials-write-script", script)
	if ctx.DryRun {
		return refresh, nil
	}

	credentials, err := ExportCloudProviderCredentials(ctx, store, alias, deps)
	if err != nil {
		return HostCredentialsRefresh{}, fmt.Errorf("export host credentials for %s: %w (run `erun cloud login --alias %s` if the SSO session lapsed)", provider.Alias, err, provider.Alias)
	}
	refresh.Expiration = credentials.Expiration
	if err := writeHostAWSCredentials(req, script, renderHostAWSCredentialsProfile(credentials, region)); err != nil {
		return HostCredentialsRefresh{}, err
	}
	return refresh, nil
}

// InspectHostAWSCredentials reads back the pod's erun-host profile so staleness
// is diagnosable at the erun layer. It reports presence and expiry only — the
// script it runs never prints credential material.
func InspectHostAWSCredentials(ctx Context, runner RemoteCommandRunnerFunc, store CloudReadStore, result OpenResult) (HostCredentialsStatus, error) {
	status := HostCredentialsStatus{
		Alias:   strings.TrimSpace(result.EnvConfig.CloudProviderAlias),
		Profile: HostAWSCredentialsProfile,
		Region:  resolveEnvironmentAWSRegion(store, result.EnvConfig),
	}
	req := ShellLaunchParamsFromResult(result)
	out, err := RunTracedRemoteCommand(ctx, runner, req, "host-credentials-read-script", hostAWSCredentialsReadScript())
	if err != nil {
		return HostCredentialsStatus{}, fmt.Errorf("read host credentials%s: %w", formatRemoteCommandStderr(out.Stderr), err)
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

func traceRuntimeExecCommand(ctx Context, args []string, label, script string) {
	traceArgs := append([]string{}, args...)
	if len(traceArgs) > 0 {
		traceArgs[len(traceArgs)-1] = "<remote-script>"
	}
	ctx.TraceCommand("", "kubectl", traceArgs...)
	ctx.TraceBlock(label, script)
}

// hostAWSCredentialsWriteArgs streams the profile to the pod on stdin: -i (not
// -it) keeps stdin a pipe for the credential bytes, and no credential value is
// ever one of these args.
func hostAWSCredentialsWriteArgs(req ShellLaunchParams, script string) []string {
	args := kubectlTargetArgs(req)
	args = append(args, "exec", "-i")
	return append(args, "deployment/"+RuntimeReleaseName(req.Tenant), "--", "/bin/sh", "-lc", script)
}

func writeHostAWSCredentials(req ShellLaunchParams, script, payload string) error {
	cmd := Command("kubectl", hostAWSCredentialsWriteArgs(req, script)...)
	cmd.Stdin = strings.NewReader(payload)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		if message := strings.TrimSpace(output.String()); message != "" {
			return fmt.Errorf("write host credentials into %s: %w: %s", RuntimeReleaseName(req.Tenant), err, message)
		}
		return fmt.Errorf("write host credentials into %s: %w", RuntimeReleaseName(req.Tenant), err)
	}
	return nil
}

// renderHostAWSCredentialsProfile builds the profile block the pod receives. The
// region rides along so the pod still resolves one from its own profile when the
// chart legitimately threads no AWS_REGION.
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

// hostAWSCredentialsWriteScript replaces the erun-host section of the pod's
// shared credentials file with the block arriving on stdin. Dropping the old
// section first is what keeps a repeated refresh an overwrite rather than a
// second copy of the profile, and every other profile in the file survives.
// The temp file is unique per invocation (mktemp, not a fixed name) so two
// refreshes racing against the same pod — `erun open` firing one while an
// explicit `erun cloud refresh` is still in flight, say — never share the
// intermediate file: each writes its own copy of the new section exactly
// once, and whichever atomic mv lands last is the profile that survives.
// A shared fixed name let both writers' appends land in the same file,
// which is how the profile ended up written twice (erun#1923).
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

// hostAWSCredentialsReadScript reports only whether the erun-host profile exists
// and when it expires. It must never print a key, a secret, or a session token.
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

// resolveEnvironmentAWSRegion resolves the AWS region an env should operate in
// when it names an AWS provider alias: the kubernetes context name, then the
// alias' Identity Center region, then the region encoded in an ECR registry
// host. Empty means nothing resolved — callers must then omit the region rather
// than pass an empty one, because an empty AWS_REGION overrides (instead of
// falling back to) whatever the pod's own profile carries.
func resolveEnvironmentAWSRegion(store CloudReadStore, env EnvConfig) string {
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

// awsRegionFromContainerRegistries reads the region out of an ECR host: a tenant
// whose images live in ECR is already operating in that region.
func awsRegionFromContainerRegistries(registries ContainerRegistries) string {
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
