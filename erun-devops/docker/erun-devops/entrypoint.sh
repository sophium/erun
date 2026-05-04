#!/bin/sh

set -eu

write_kubeconfig() {
    kube_dir="${HOME}/.kube"
    kubeconfig_path="${KUBECONFIG:-${kube_dir}/config}"
    mkdir -p "${kube_dir}"

    if [ -n "${KUBERNETES_SERVICE_HOST:-}" ]; then
        token_file=/var/run/secrets/kubernetes.io/serviceaccount/token
        ca_file=/var/run/secrets/kubernetes.io/serviceaccount/ca.crt
        namespace_file=/var/run/secrets/kubernetes.io/serviceaccount/namespace

        if [ ! -r "${token_file}" ] || [ ! -r "${ca_file}" ] || [ ! -r "${namespace_file}" ]; then
            return
        fi

        namespace=$(cat "${namespace_file}")
        server="https://${KUBERNETES_SERVICE_HOST}:${KUBERNETES_SERVICE_PORT_HTTPS:-443}"

        cat >"${kubeconfig_path}" <<EOF
apiVersion: v1
kind: Config
clusters:
  - cluster:
      certificate-authority: ${ca_file}
      server: ${server}
    name: in-cluster
contexts:
  - context:
      cluster: in-cluster
      namespace: ${namespace}
      user: erun-devops
    name: in-cluster
current-context: in-cluster
users:
  - name: erun-devops
    user:
      tokenFile: ${token_file}
EOF
        return
    fi

    if [ -n "${ERUN_HOST_KUBE_CONFIG:-}" ] && [ -r "${ERUN_HOST_KUBE_CONFIG}" ]; then
        sed \
            -e 's#https://127\.0\.0\.1:#https://host.docker.internal:#g' \
            -e 's#https://localhost:#https://host.docker.internal:#g' \
            "${ERUN_HOST_KUBE_CONFIG}" >"${kubeconfig_path}"
    fi
}

runtime_repo_dir() {
    printf '%s\n' "${ERUN_REPO_PATH:-${HOME}/git/erun}"
}

runtime_repo_is_remote() {
    case "${ERUN_REPO_REMOTE:-}" in
        1|true|TRUE|True|yes|YES|on|ON)
            return 0
            ;;
    esac
    return 1
}

runtime_cloud_environment() {
    case "${ERUN_CLOUD_ENVIRONMENT:-}" in
        1|true|TRUE|True|yes|YES|on|ON)
            return 0
            ;;
    esac
    return 1
}

runtime_cloud_provider() {
    if [ -n "${ERUN_CLOUD_PROVIDER:-}" ]; then
        printf '%s\n' "${ERUN_CLOUD_PROVIDER}"
        return
    fi
    case "${ERUN_CLOUD_PROVIDER_ALIAS:-}" in
        *@aws)
            printf '%s\n' "aws"
            ;;
    esac
}

runtime_namespace() {
    if [ -n "${ERUN_NAMESPACE:-}" ]; then
        printf '%s\n' "${ERUN_NAMESPACE}"
        return
    fi

    namespace_file=/var/run/secrets/kubernetes.io/serviceaccount/namespace
    if [ -r "${namespace_file}" ]; then
        cat "${namespace_file}"
    fi
}

imds_token() {
    curl -fsS -m 2 -X PUT "http://169.254.169.254/latest/api/token" \
        -H "X-aws-ec2-metadata-token-ttl-seconds: 60" 2>/dev/null || true
}

imds_get() {
    path="${1:-}"
    if [ -z "${path}" ]; then
        return
    fi

    token=$(imds_token)
    if [ -n "${token}" ]; then
        curl -fsS -m 2 -H "X-aws-ec2-metadata-token: ${token}" "http://169.254.169.254/latest/${path}" 2>/dev/null || true
        return
    fi
    curl -fsS -m 2 "http://169.254.169.254/latest/${path}" 2>/dev/null || true
}

runtime_cloud_instance_id() {
    if [ -n "${ERUN_CLOUD_INSTANCE_ID:-}" ]; then
        printf '%s\n' "${ERUN_CLOUD_INSTANCE_ID}"
        return
    fi
    imds_get "meta-data/instance-id"
}

runtime_cloud_region() {
    if [ -n "${ERUN_CLOUD_REGION:-}" ]; then
        printf '%s\n' "${ERUN_CLOUD_REGION}"
        return
    fi

    imds_get "dynamic/instance-identity/document" | sed -n 's/.*"region"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1
}

stop_cloud_host() {
    if ! runtime_cloud_environment; then
        return 0
    fi
    if ! command -v aws >/dev/null 2>&1; then
        echo "aws CLI is not installed; cannot stop cloud host" >&2
        return 1
    fi

    region=$(runtime_cloud_region)
    instance_id=$(runtime_cloud_instance_id)
    if [ -z "${region}" ] || [ -z "${instance_id}" ]; then
        echo "cloud host region or instance id is not available; cannot stop cloud host" >&2
        return 1
    fi

    aws --cli-connect-timeout 5 --cli-read-timeout 20 ec2 stop-instances --region "${region}" --instance-ids "${instance_id}" >/dev/null
}

runtime_sshd_enabled() {
    case "${ERUN_SSHD_ENABLED:-}" in
        1|true|TRUE|True|yes|YES|on|ON)
            return 0
            ;;
    esac
    return 1
}

activity_args() {
    tenant="${ERUN_TENANT:-}"
    environment="${ERUN_ENVIRONMENT:-}"
    if [ -z "${tenant}" ] || [ -z "${environment}" ]; then
        return 1
    fi
    printf '%s\n' "--tenant" "${tenant}" "--environment" "${environment}"
}

record_activity() {
    kind="${1:-}"
    shift || true
    args=$(activity_args) || return 0
    # shellcheck disable=SC2086
    erun activity touch ${args} --kind "${kind}" "$@" >/dev/null 2>&1 || true
}

initialize_erun_config() {
    repo_dir=$(runtime_repo_dir)
    tenant="${ERUN_TENANT:-}"
    environment="${ERUN_ENVIRONMENT:-}"
    config_home="${XDG_CONFIG_HOME:-${HOME}/.config}"
    config_dir="${config_home}/erun"
    cloud_provider=$(runtime_cloud_provider)
    cloud_provider_alias="${ERUN_CLOUD_PROVIDER_ALIAS:-}"
    cloud_region=""
    cloud_instance_id=""
    cloud_context_name="${ERUN_CLOUD_CONTEXT_NAME:-${ERUN_KUBERNETES_CONTEXT:-in-cluster}}"
    env_remote_line=""
    env_managed_cloud_line=""
    env_cloud_provider_alias_line=""

    if [ -z "${tenant}" ] || [ -z "${environment}" ]; then
        return
    fi
    if [ -n "${cloud_provider}" ]; then
        cloud_region=$(runtime_cloud_region)
        cloud_instance_id=$(runtime_cloud_instance_id)
    fi

    if runtime_repo_is_remote; then
        env_remote_line="remote: true"
    fi
    if [ -n "${cloud_provider_alias}" ]; then
        env_cloud_provider_alias_line="cloudprovideralias: ${cloud_provider_alias}"
    fi
    if runtime_cloud_environment || { runtime_repo_is_remote && [ -n "${cloud_provider}" ] && [ -n "${cloud_provider_alias}" ] && [ -n "${cloud_region}" ]; }; then
        env_managed_cloud_line="managedcloud: true"
    fi

    mkdir -p "${config_dir}/${tenant}/${environment}"

    cat >"${config_dir}/config.yaml" <<EOF
defaulttenant: ${tenant}
EOF
    if [ -n "${cloud_provider}" ] && [ -n "${cloud_provider_alias}" ]; then
        cloud_username=""
        cloud_account_id=""
        case "${cloud_provider_alias}" in
            *+*@*)
                cloud_username="${cloud_provider_alias%%+*}"
                cloud_account_part="${cloud_provider_alias#*+}"
                cloud_account_id="${cloud_account_part%@*}"
                ;;
        esac
        cat >>"${config_dir}/config.yaml" <<EOF
cloudproviders:
  - alias: ${cloud_provider_alias}
    provider: ${cloud_provider}
EOF
        if [ -n "${cloud_username}" ]; then
            cat >>"${config_dir}/config.yaml" <<EOF
    username: ${cloud_username}
EOF
        fi
        if [ -n "${cloud_account_id}" ]; then
            cat >>"${config_dir}/config.yaml" <<EOF
    accountid: "${cloud_account_id}"
EOF
        fi
        if [ -n "${cloud_region}" ]; then
            cat >>"${config_dir}/config.yaml" <<EOF
cloudcontexts:
  - name: ${cloud_context_name}
    provider: ${cloud_provider}
    cloudprovideralias: ${cloud_provider_alias}
    region: ${cloud_region}
    kubernetescontext: ${ERUN_KUBERNETES_CONTEXT:-in-cluster}
    status: running
EOF
            if [ -n "${cloud_instance_id}" ]; then
                cat >>"${config_dir}/config.yaml" <<EOF
    instanceid: ${cloud_instance_id}
EOF
            fi
        fi
    fi

    cat >"${config_dir}/${tenant}/config.yaml" <<EOF
projectroot: ${repo_dir}
name: ${tenant}
defaultenvironment: ${environment}
EOF

    cat >"${config_dir}/${tenant}/${environment}/config.yaml" <<EOF
name: ${environment}
repopath: ${repo_dir}
kubernetescontext: ${ERUN_KUBERNETES_CONTEXT:-in-cluster}
${env_remote_line}
${env_cloud_provider_alias_line}
${env_managed_cloud_line}
idle:
  timeout: ${ERUN_IDLE_TIMEOUT:-5m0s}
  workinghours: ${ERUN_IDLE_WORKING_HOURS:-08:00-20:00}
  idletrafficbytes: ${ERUN_IDLE_TRAFFIC_BYTES:-0}
EOF
}

initialize_codex_config() {
    codex_configure="${HOME}/.erun/configure-codex-mcp.sh"

    mkdir -p "$(dirname "${codex_configure}")"
    cat >"${codex_configure}" <<'CODEX_CONFIG_SCRIPT'
#!/bin/sh
set -eu

codex_dir="${HOME}/.codex"
codex_config="${codex_dir}/config.toml"
mcp_url="http://127.0.0.1:${ERUN_MCP_PORT:-17000}${ERUN_MCP_PATH:-/mcp}"

mkdir -p "${codex_dir}"
touch "${codex_config}"

tmp_config="${codex_config}.tmp"
awk '
    function write_codex_policy() {
        if (!wrote_policy) {
            print ""
            print "sandbox_mode = \"danger-full-access\""
            print "approval_policy = \"on-request\""
            wrote_policy = 1
        }
    }
    /^sandbox_mode = / { next }
    /^approval_policy = / { next }
    /^\[mcp_servers\.erun\]$/ { skip = 1; next }
    /^\[/ && skip { skip = 0 }
    /^\[/ && !skip { write_codex_policy() }
    !skip { print }
    END { write_codex_policy() }
' "${codex_config}" >"${tmp_config}"
mv "${tmp_config}" "${codex_config}"

cat >>"${codex_config}" <<EOF

[mcp_servers.erun]
url = "${mcp_url}"
tool_timeout_sec = 600
EOF
CODEX_CONFIG_SCRIPT
    chmod 700 "${codex_configure}"
    "${codex_configure}" >/dev/null 2>&1 || true
    install_shell_profile_hook "${HOME}/.bashrc"
    install_shell_profile_hook "${HOME}/.profile"
    if [ -f "${HOME}/.bash_profile" ]; then
        install_shell_profile_hook "${HOME}/.bash_profile"
    fi
}

initialize_claude_config() {
    claude_configure="${HOME}/.erun/configure-claude-code.sh"

    mkdir -p "$(dirname "${claude_configure}")"
    cat >"${claude_configure}" <<'CLAUDE_CONFIG_SCRIPT'
#!/bin/sh
set -eu

imds_get() {
    path="${1:-}"
    if [ -z "${path}" ]; then
        return
    fi
    token=$(curl -fsS -m 2 -X PUT "http://169.254.169.254/latest/api/token" \
        -H "X-aws-ec2-metadata-token-ttl-seconds: 60" 2>/dev/null || true)
    if [ -n "${token}" ]; then
        curl -fsS -m 2 -H "X-aws-ec2-metadata-token: ${token}" "http://169.254.169.254/latest/${path}" 2>/dev/null || true
        return
    fi
    curl -fsS -m 2 "http://169.254.169.254/latest/${path}" 2>/dev/null || true
}

imds_region() {
    imds_get "dynamic/instance-identity/document" | sed -n 's/.*"region"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1
}

cloud_provider="${ERUN_CLOUD_PROVIDER:-}"
if [ -z "${cloud_provider}" ]; then
    case "${ERUN_CLOUD_PROVIDER_ALIAS:-}" in
        *@aws)
            cloud_provider=aws
            ;;
    esac
fi

configure_bedrock=0
if [ "${cloud_provider}" != "aws" ] && [ -z "${CLAUDE_CODE_USE_BEDROCK:-}" ] && [ -z "${CLAUDE_CODE_USE_MANTLE:-}" ]; then
    configure_bedrock=0
else
    configure_bedrock=1
fi

claude_region="${AWS_REGION:-${ERUN_CLOUD_REGION:-}}"
if [ -z "${claude_region}" ] && [ "${cloud_provider}" = "aws" ]; then
    claude_region=$(imds_region)
fi
if [ -z "${claude_region}" ]; then
    configure_bedrock=0
fi

claude_dir="${HOME}/.claude"
claude_settings="${claude_dir}/settings.json"
claude_state="${HOME}/.claude.json"
claude_project_path="${ERUN_REPO_PATH:-${HOME}/git/erun}"
claude_mcp_url="http://127.0.0.1:${ERUN_MCP_PORT:-17000}${ERUN_MCP_PATH:-/mcp}"
mkdir -p "${claude_dir}"

CLAUDE_SETTINGS_PATH="${claude_settings}" \
CLAUDE_STATE_PATH="${claude_state}" \
ERUN_CLAUDE_CONFIGURE_BEDROCK="${configure_bedrock}" \
ERUN_CLAUDE_REGION="${claude_region}" \
ERUN_CLAUDE_PROJECT_PATH="${claude_project_path}" \
ERUN_CLAUDE_MCP_URL="${claude_mcp_url}" \
node <<'NODE'
const fs = require('fs');

const settingsPath = process.env.CLAUDE_SETTINGS_PATH;
const statePath = process.env.CLAUDE_STATE_PATH;
const configureBedrock = process.env.ERUN_CLAUDE_CONFIGURE_BEDROCK === '1';
const region = (process.env.ERUN_CLAUDE_REGION || '').trim();

function readJSON(path) {
  try {
    const parsed = JSON.parse(fs.readFileSync(path, 'utf8'));
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      return parsed;
    }
  } catch (_) {
  }
  return {};
}

function writeJSON(path, value) {
  fs.writeFileSync(path, `${JSON.stringify(value, null, 2)}\n`, { mode: 0o600 });
}

function envValue(name, fallback = '') {
  return (process.env[name] || fallback || '').trim();
}

function ensureObject(parent, name) {
  if (!parent[name] || typeof parent[name] !== 'object' || Array.isArray(parent[name])) {
    parent[name] = {};
  }
  return parent[name];
}

function setEnv(settings, name, value) {
  const normalized = (value || '').trim();
  if (!normalized) {
    return;
  }
  settings.env[name] = normalized;
}

function listValue(value) {
  const result = [];
  const seen = new Set();
  for (const entry of (value || '').split(',')) {
    const normalized = entry.trim();
    const key = normalized.toLowerCase();
    if (!normalized || seen.has(key)) {
      continue;
    }
    seen.add(key);
    result.push(normalized);
  }
  return result;
}

if (configureBedrock) {
  const settings = readJSON(settingsPath);
  settings.$schema = settings.$schema || 'https://json.schemastore.org/claude-code-settings.json';
  settings.env = ensureObject(settings, 'env');

  setEnv(settings, 'CLAUDE_CODE_USE_BEDROCK', envValue('CLAUDE_CODE_USE_BEDROCK', '1'));
  setEnv(settings, 'CLAUDE_CODE_USE_MANTLE', envValue('CLAUDE_CODE_USE_MANTLE', '1'));
  setEnv(settings, 'AWS_REGION', region);
  setEnv(settings, 'ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION', envValue('ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION', region));
  setEnv(settings, 'CLAUDE_CODE_MAX_OUTPUT_TOKENS', envValue('CLAUDE_CODE_MAX_OUTPUT_TOKENS', '4096'));
  setEnv(settings, 'MAX_THINKING_TOKENS', envValue('MAX_THINKING_TOKENS', '1024'));

  for (const name of [
    'AWS_PROFILE',
    'ANTHROPIC_MODEL',
    'ANTHROPIC_DEFAULT_OPUS_MODEL',
    'ANTHROPIC_DEFAULT_SONNET_MODEL',
    'ANTHROPIC_DEFAULT_HAIKU_MODEL',
    'ANTHROPIC_BEDROCK_BASE_URL',
    'ANTHROPIC_BEDROCK_MANTLE_BASE_URL',
    'ANTHROPIC_BEDROCK_SERVICE_TIER',
    'CLAUDE_CODE_SKIP_MANTLE_AUTH',
    'DISABLE_PROMPT_CACHING',
    'ENABLE_PROMPT_CACHING_1H',
  ]) {
    setEnv(settings, name, envValue(name));
  }

  const availableModels = listValue(envValue('ERUN_CLAUDE_AVAILABLE_MODELS'));
  if (availableModels.length > 0) {
    settings.availableModels = availableModels;
  }

  writeJSON(settingsPath, settings);
}

const projectPath = envValue('ERUN_CLAUDE_PROJECT_PATH');
const mcpURL = envValue('ERUN_CLAUDE_MCP_URL');
if (statePath && projectPath && mcpURL) {
  const state = readJSON(statePath);
  const projects = ensureObject(state, 'projects');
  const project = ensureObject(projects, projectPath);
  const mcpServers = ensureObject(project, 'mcpServers');
  mcpServers.erun = {
    type: 'http',
    url: mcpURL,
  };
  writeJSON(statePath, state);
}
NODE
    chmod 600 "${claude_settings}" >/dev/null 2>&1 || true
    chmod 600 "${claude_state}" >/dev/null 2>&1 || true
CLAUDE_CONFIG_SCRIPT
    chmod 700 "${claude_configure}"
    "${claude_configure}" >/dev/null 2>&1 || true
    install_shell_profile_hook "${HOME}/.bashrc"
    install_shell_profile_hook "${HOME}/.profile"
    if [ -f "${HOME}/.bash_profile" ]; then
        install_shell_profile_hook "${HOME}/.bash_profile"
    fi
}

initialize_shell_activity_config() {
    rc_file="${HOME}/.erun-shell-activity.bashrc"
    bashrc_file="${HOME}/.bashrc"
    cat >"${rc_file}" <<'EOF'
if [ -r "${HOME}/.bashrc" ]; then
    . "${HOME}/.bashrc"
fi
EOF
    install_shell_profile_hook "${bashrc_file}"
    printf '%s\n' "${rc_file}"
}

install_shell_profile_hook() {
    bashrc_file="${1}"
    hook_file="${HOME}/.erun-shell-hook.bashrc"
    cat >"${hook_file}" <<'EOF'
if [ -x "${HOME}/.erun/configure-codex-mcp.sh" ]; then
    "${HOME}/.erun/configure-codex-mcp.sh" >/dev/null 2>&1 || true
fi

if [ -x "${HOME}/.erun/configure-claude-code.sh" ]; then
    "${HOME}/.erun/configure-claude-code.sh" >/dev/null 2>&1 || true
fi

__erun_record_cli_activity() {
    if [ -n "${ERUN_TENANT:-}" ] && [ -n "${ERUN_ENVIRONMENT:-}" ]; then
        command erun activity touch --tenant "${ERUN_TENANT}" --environment "${ERUN_ENVIRONMENT}" --kind cli >/dev/null 2>&1 || true
    fi
}

case ";${PROMPT_COMMAND:-};" in
    *";__erun_record_cli_activity;"*) ;;
    *) PROMPT_COMMAND="__erun_record_cli_activity${PROMPT_COMMAND:+;${PROMPT_COMMAND}}" ;;
esac
EOF

    touch "${bashrc_file}"
    tmp_bashrc="${bashrc_file}.tmp"
    awk '
        /^# >>> erun shell hook >>>$/ { skip = 1; next }
        /^# <<< erun shell hook <<<$/{ skip = 0; next }
        !skip { print }
    ' "${bashrc_file}" >"${tmp_bashrc}"
    cat >>"${tmp_bashrc}" <<EOF
# >>> erun shell hook >>>
if [ -r "${hook_file}" ]; then
    . "${hook_file}"
fi
# <<< erun shell hook <<<
EOF
    mv "${tmp_bashrc}" "${bashrc_file}"
}

start_sshd() {
    if ! runtime_sshd_enabled; then
        return
    fi

    sshd_dir="${HOME}/.sshd"
    host_key_dir="${sshd_dir}/host_keys"
    pid_file="${sshd_dir}/sshd.pid"
    proxy_pid_file="${sshd_dir}/ssh-proxy.pid"
    proxy_log_file="${sshd_dir}/ssh-proxy.log"
    config_file="${sshd_dir}/sshd_config"
    sshd_port="17023"
    proxy_port="${ERUN_SSHD_PORT:-17022}"
    mkdir -p "${HOME}/.ssh" "${host_key_dir}"
    chmod 700 "${HOME}/.ssh" "${sshd_dir}" "${host_key_dir}"

    if [ ! -r "${pid_file}" ] || ! kill -0 "$(cat "${pid_file}")" 2>/dev/null; then
        rm -f "${pid_file}"

        host_key="${host_key_dir}/ssh_host_ed25519_key"
        if [ ! -f "${host_key}" ]; then
            ssh-keygen -q -t ed25519 -N "" -f "${host_key}" >/dev/null 2>&1
        fi
        chmod 600 "${host_key}"
        chmod 644 "${host_key}.pub"

        cat >"${config_file}" <<EOF
Port ${sshd_port}
ListenAddress 127.0.0.1
HostKey ${host_key}
AuthorizedKeysFile ${HOME}/.ssh/authorized_keys
PasswordAuthentication no
KbdInteractiveAuthentication no
ChallengeResponseAuthentication no
PubkeyAuthentication yes
StrictModes no
PermitRootLogin no
UsePAM no
PidFile ${pid_file}
PrintMotd no
Subsystem sftp internal-sftp
EOF
        chmod 600 "${config_file}"
        touch "${HOME}/.ssh/authorized_keys"
        chmod 600 "${HOME}/.ssh/authorized_keys"

        /usr/sbin/sshd -f "${config_file}" -E "${sshd_dir}/sshd.log"
    fi

    if [ -r "${proxy_pid_file}" ] && kill -0 "$(cat "${proxy_pid_file}")" 2>/dev/null; then
        return
    fi
    rm -f "${proxy_pid_file}"
    touch "${proxy_log_file}"
    erun activity ssh-proxy \
        --tenant "${ERUN_TENANT:-}" \
        --environment "${ERUN_ENVIRONMENT:-}" \
        --listen "0.0.0.0:${proxy_port}" \
        --target "127.0.0.1:${sshd_port}" \
        --idle-traffic-bytes "${ERUN_IDLE_TRAFFIC_BYTES:-0}" \
        >>"${proxy_log_file}" 2>&1 &
    echo "$!" >"${proxy_pid_file}"
}

start_environment_idle_monitor() {
    if ! runtime_cloud_environment; then
        return
    fi
    if [ -z "${ERUN_TENANT:-}" ] || [ -z "${ERUN_ENVIRONMENT:-}" ]; then
        return
    fi

    (
        while :; do
            sleep 30
            if erun activity stop-ready --tenant "${ERUN_TENANT}" --environment "${ERUN_ENVIRONMENT}" >/dev/null 2>&1; then
                namespace=$(runtime_namespace)
                if [ -n "${namespace}" ]; then
                    kubectl --context "${ERUN_KUBERNETES_CONTEXT:-in-cluster}" --namespace "${namespace}" scale "deployment/${ERUN_RUNTIME_DEPLOYMENT:-erun-devops}" --replicas=0 >/dev/null 2>&1 || true
                fi
                mkdir -p "${HOME}/.erun"
                stop_cloud_host >>"${HOME}/.erun/idle-stop.log" 2>&1 || true
                exit 0
            fi
        done
    ) &
}

run_shell() {
    repo_dir=$(runtime_repo_dir)

    if [ -d "${repo_dir}" ]; then
        cd "${repo_dir}"
    fi

    shell_activity_rc=$(initialize_shell_activity_config)
    if [ -n "${shell_activity_rc}" ]; then
        exec /bin/bash --rcfile "${shell_activity_rc}" -i
    fi
    exec /bin/bash -i
}

write_kubeconfig
start_sshd
start_environment_idle_monitor

if [ "${1:-}" = "shell" ]; then
    shift
    initialize_erun_config
    initialize_codex_config
    initialize_claude_config
    record_activity cli
    run_shell "$@"
fi

if [ "${1:-}" = "api" ]; then
    shift
    initialize_erun_config
    record_activity api
    echo "starting erun API on ${ERUN_API_HOST:-0.0.0.0}:${ERUN_API_PORT:-17033}"
    if [ -n "${ERUN_AWS_IDENTITY_STORE_REGION:-}" ]; then
        set -- --aws-identity-store-region "${ERUN_AWS_IDENTITY_STORE_REGION}" "$@"
    fi
    if [ -n "${ERUN_AWS_IDENTITY_STORE_ID:-}" ]; then
        set -- --aws-identity-store-id "${ERUN_AWS_IDENTITY_STORE_ID}" "$@"
    fi
    if [ -n "${ERUN_OIDC_ALLOWED_ISSUERS:-}" ]; then
        set -- --oidc-allowed-issuers "${ERUN_OIDC_ALLOWED_ISSUERS}" "$@"
    fi
    exec eapi \
        --host "${ERUN_API_HOST:-0.0.0.0}" \
        --port "${ERUN_API_PORT:-17033}" \
        "$@"
fi

if [ "${1:-}" = "mcp" ]; then
    shift
    initialize_erun_config
    initialize_codex_config
    initialize_claude_config
    record_activity mcp

    set -- emcp "$@" \
        --host "${ERUN_MCP_HOST:-0.0.0.0}" \
        --port "${ERUN_MCP_PORT:-17000}" \
        --path "${ERUN_MCP_PATH:-/mcp}" \
        --tenant "${ERUN_TENANT:-}" \
        --environment "${ERUN_ENVIRONMENT:-}" \
        --repo-path "$(runtime_repo_dir)" \
        --kubernetes-context "${ERUN_KUBERNETES_CONTEXT:-in-cluster}"

    namespace=$(runtime_namespace)
    if [ -n "${namespace}" ]; then
        set -- "$@" --namespace "${namespace}"
    fi

    echo "starting erun MCP on ${ERUN_MCP_HOST:-0.0.0.0}:${ERUN_MCP_PORT:-17000}${ERUN_MCP_PATH:-/mcp}"
    exec "$@"
fi

if [ "${1:-}" = "devops" ] || [ "$#" -eq 0 ]; then
    initialize_erun_config
    initialize_codex_config
    initialize_claude_config
    record_activity devops
    exec sleep infinity
fi

exec "$@"
