package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSSHHostAlias(t *testing.T) {
	if got := SSHHostAlias("Tenant_A", "Remote Dev"); got != "erun-tenant-a-remote-dev" {
		t.Fatalf("unexpected SSH host alias: %q", got)
	}
}

func TestSSHPrivateKeyPath(t *testing.T) {
	if got := SSHPrivateKeyPath("/tmp/id_ed25519.pub"); got != "/tmp/id_ed25519" {
		t.Fatalf("unexpected private key path: %q", got)
	}
	if got := SSHPrivateKeyPath("/tmp/custom-key"); got != "" {
		t.Fatalf("expected empty private key path, got %q", got)
	}
}

func TestRuntimeDockerfileUnlocksSSHUserAccount(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "erun-devops", "docker", "erun-devops", "Dockerfile"))
	if err != nil {
		t.Fatalf("read runtime Dockerfile: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "passwd -d erun") {
		t.Fatalf("expected runtime Dockerfile to unlock erun account for SSH public-key auth, got:\n%s", content)
	}
}

func TestRuntimeDockerfileUsesDesktopGoVersion(t *testing.T) {
	dockerfileData, err := os.ReadFile(filepath.Join("..", "erun-devops", "docker", "erun-devops", "Dockerfile"))
	if err != nil {
		t.Fatalf("read runtime Dockerfile: %v", err)
	}
	goModData, err := os.ReadFile(filepath.Join("..", "erun-ui", "go.mod"))
	if err != nil {
		t.Fatalf("read erun-ui go.mod: %v", err)
	}

	version, err := moduleGoVersion(string(goModData))
	if err != nil {
		t.Fatalf("resolve erun-ui Go version: %v", err)
	}
	expected := "FROM golang:" + version + " AS builder"
	if !strings.Contains(string(dockerfileData), expected) {
		t.Fatalf("expected runtime Dockerfile to use desktop Go version %q, got:\n%s", expected, string(dockerfileData))
	}
}

func TestRuntimeDockerfileInstallsAptPackagesBeforeBuilderArtifacts(t *testing.T) {
	dockerfileData, err := os.ReadFile(filepath.Join("..", "erun-devops", "docker", "erun-devops", "Dockerfile"))
	if err != nil {
		t.Fatalf("read runtime Dockerfile: %v", err)
	}
	content := string(dockerfileData)

	aptIndex := strings.Index(content, "RUN apt-get update &&")
	if aptIndex < 0 {
		t.Fatalf("expected runtime Dockerfile apt-get layer, got:\n%s", content)
	}
	goCopyIndex := strings.Index(content, "COPY --from=builder /usr/local/go /usr/local/go")
	if goCopyIndex < 0 {
		t.Fatalf("expected runtime Dockerfile to copy Go toolchain from builder, got:\n%s", content)
	}
	if aptIndex > goCopyIndex {
		t.Fatalf("expected runtime Dockerfile to install apt packages before copying builder artifacts so cache survives source rebuilds, got:\n%s", content)
	}
}

func TestRuntimeDockerfileInstallsPinnedCodexCLI(t *testing.T) {
	dockerfileData, err := os.ReadFile(filepath.Join("..", "erun-devops", "docker", "erun-devops", "Dockerfile"))
	if err != nil {
		t.Fatalf("read runtime Dockerfile: %v", err)
	}
	content := string(dockerfileData)

	if !strings.Contains(content, "ARG NODE_VERSION=24.14.0") {
		t.Fatalf("expected runtime Dockerfile to pin the Node.js version for Codex CLI, got:\n%s", content)
	}
	if !strings.Contains(content, "ARG CODEX_VERSION=0.125.0") {
		t.Fatalf("expected runtime Dockerfile to pin the Codex CLI version, got:\n%s", content)
	}
	if !strings.Contains(content, "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${node_arch}.tar.gz") {
		t.Fatalf("expected runtime Dockerfile to install Node.js from a pinned upstream tarball, got:\n%s", content)
	}
	if !strings.Contains(content, "npm install -g \"@openai/codex@${CODEX_VERSION}\"") {
		t.Fatalf("expected runtime Dockerfile to install the pinned Codex CLI globally, got:\n%s", content)
	}
	if !strings.Contains(content, "bubblewrap") {
		t.Fatalf("expected runtime Dockerfile to install bubblewrap for Codex CLI sandboxing, got:\n%s", content)
	}
	for _, want := range []string{
		"ARG AWS_CLI_VERSION=",
		"aws_arch=x86_64",
		"aws_arch=aarch64",
		"https://awscli.amazonaws.com/awscli-exe-linux-${aws_arch}-${AWS_CLI_VERSION}.zip",
		"/tmp/aws/install --bin-dir /usr/local/bin --install-dir /usr/local/aws-cli",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected runtime Dockerfile to install pinned AWS CLI v2 with %q, got:\n%s", want, content)
		}
	}
}

func TestRuntimeEntrypointDisablesStrictModesForPVCBackedHome(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "erun-devops", "docker", "erun-devops", "entrypoint.sh"))
	if err != nil {
		t.Fatalf("read runtime entrypoint: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "StrictModes no") {
		t.Fatalf("expected runtime entrypoint to disable sshd strict modes for PVC-backed home directory, got:\n%s", string(data))
	}
	if !strings.Contains(content, `sshd_port="17023"`) || !strings.Contains(content, `proxy_port="${ERUN_SSHD_PORT:-17022}"`) {
		t.Fatalf("expected runtime entrypoint to run sshd behind the configured SSH proxy, got:\n%s", content)
	}
	if !strings.Contains(content, `erun activity ssh-proxy`) || !strings.Contains(content, `--listen "0.0.0.0:${proxy_port}"`) || !strings.Contains(content, `--target "127.0.0.1:${sshd_port}"`) {
		t.Fatalf("expected runtime entrypoint to run the ERun activity proxy in front of sshd, got:\n%s", content)
	}
}

func TestRuntimeEntrypointConfiguresCodexMCP(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "erun-devops", "docker", "erun-devops", "entrypoint.sh"))
	if err != nil {
		t.Fatalf("read runtime entrypoint: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		`initialize_codex_config`,
		`configure-codex-mcp.sh`,
		`install_shell_profile_hook "${HOME}/.bashrc"`,
		`install_shell_profile_hook "${HOME}/.profile"`,
		`function write_codex_policy()`,
		`/^sandbox_mode = / { next }`,
		`/^approval_policy = / { next }`,
		`/^\[/ && !skip { write_codex_policy() }`,
		`END { write_codex_policy() }`,
		`print "sandbox_mode = \"danger-full-access\""`,
		`print "approval_policy = \"on-request\""`,
		`[mcp_servers.erun]`,
		`url = "${mcp_url}"`,
		`tool_timeout_sec = 600`,
		`http://127.0.0.1:${ERUN_MCP_PORT:-17000}${ERUN_MCP_PATH:-/mcp}`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected runtime entrypoint to contain %q, got:\n%s", want, content)
		}
	}
}

func TestRuntimeEntrypointPassesOIDCIssuersToAPI(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "erun-devops", "docker", "erun-devops", "entrypoint.sh"))
	if err != nil {
		t.Fatalf("read runtime entrypoint: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		`if [ -n "${ERUN_OIDC_ALLOWED_ISSUERS:-}" ]; then`,
		`--oidc-allowed-issuers "${ERUN_OIDC_ALLOWED_ISSUERS}"`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected runtime entrypoint to contain %q, got:\n%s", want, content)
		}
	}
}

func TestRuntimeEntrypointStopsCloudHostAfterIdle(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "erun-devops", "docker", "erun-devops", "entrypoint.sh"))
	if err != nil {
		t.Fatalf("read runtime entrypoint: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"stop_cloud_host()",
		`runtime_cloud_instance_id()`,
		`runtime_cloud_region()`,
		`aws --cli-connect-timeout 5 --cli-read-timeout 20 ec2 stop-instances --region "${region}" --instance-ids "${instance_id}"`,
		`kubectl --context "${ERUN_KUBERNETES_CONTEXT:-in-cluster}" --namespace "${namespace}" scale "deployment/${ERUN_RUNTIME_DEPLOYMENT:-erun-devops}" --replicas=0`,
		`stop_cloud_host >>"${HOME}/.erun/idle-stop.log" 2>&1 || true`,
		`http://169.254.169.254/latest/${path}`,
		`imds_get "meta-data/instance-id"`,
		`imds_get "dynamic/instance-identity/document"`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected runtime entrypoint to contain %q, got:\n%s", want, content)
		}
	}
}

func TestRuntimeCodexWrapperPreservesForegroundTTY(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "erun-devops", "docker", "erun-devops", "codex-wrapper.sh"))
	if err != nil {
		t.Fatalf("read runtime Codex wrapper: %v", err)
	}
	content := string(data)
	if strings.Contains(content, `codex-real "$@" &`) {
		t.Fatalf("expected Codex wrapper to run codex-real in the foreground, got:\n%s", content)
	}
	for _, want := range []string{
		`monitor_codex_log()`,
		`script -q -f -e -c "${command}" "${log_file}"`,
		`touch_codex --seen`,
		`touch_codex --bytes "${delta}"`,
		`codex-real "$@"`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected Codex wrapper to contain %q, got:\n%s", want, content)
		}
	}
}

func TestRuntimeEntrypointWritesRemoteOnlyToEnvironmentConfig(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "erun-devops", "docker", "erun-devops", "entrypoint.sh"))
	if err != nil {
		t.Fatalf("read runtime entrypoint: %v", err)
	}
	content := string(data)
	tenantConfig := `cat >"${config_dir}/${tenant}/config.yaml" <<EOF
projectroot: ${repo_dir}
name: ${tenant}
defaultenvironment: ${environment}
EOF`
	if !strings.Contains(content, tenantConfig) {
		t.Fatalf("expected tenant config heredoc without remote flag, got:\n%s", content)
	}
	envConfig := `cat >"${config_dir}/${tenant}/${environment}/config.yaml" <<EOF
name: ${environment}
repopath: ${repo_dir}
kubernetescontext: ${ERUN_KUBERNETES_CONTEXT:-in-cluster}
${env_remote_line}
${env_managed_cloud_line}
idle:
  timeout: ${ERUN_IDLE_TIMEOUT:-5m0s}
  workinghours: ${ERUN_IDLE_WORKING_HOURS:-08:00-20:00}
  idletrafficbytes: ${ERUN_IDLE_TRAFFIC_BYTES:-0}
EOF`
	if !strings.Contains(content, envConfig) {
		t.Fatalf("expected environment config heredoc to include env remote flag, got:\n%s", content)
	}
}

func TestRuntimeEntrypointRecordsInteractiveShellActivity(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "erun-devops", "docker", "erun-devops", "entrypoint.sh"))
	if err != nil {
		t.Fatalf("read runtime entrypoint: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"initialize_shell_activity_config()",
		"__erun_record_cli_activity()",
		"PROMPT_COMMAND=\"__erun_record_cli_activity",
		"exec /bin/bash --rcfile \"${shell_activity_rc}\" -i",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("expected runtime entrypoint to contain %q, got:\n%s", want, content)
		}
	}
}

func moduleGoVersion(goMod string) (string, error) {
	for _, line := range strings.Split(goMod, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "go ") {
			continue
		}
		version := strings.TrimSpace(strings.TrimPrefix(line, "go "))
		if version != "" {
			return version, nil
		}
	}
	return "", fmt.Errorf("go directive not found")
}
