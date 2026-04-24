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

runtime_sshd_enabled() {
    case "${ERUN_SSHD_ENABLED:-}" in
        1|true|TRUE|True|yes|YES|on|ON)
            return 0
            ;;
    esac
    return 1
}

initialize_erun_config() {
    repo_dir=$(runtime_repo_dir)
    tenant="${ERUN_TENANT:-}"
    environment="${ERUN_ENVIRONMENT:-}"
    config_home="${XDG_CONFIG_HOME:-${HOME}/.config}"
    config_dir="${config_home}/erun"
    env_remote_line=""

    if [ -z "${tenant}" ] || [ -z "${environment}" ]; then
        return
    fi

    if runtime_repo_is_remote; then
        env_remote_line="remote: true"
    fi

    mkdir -p "${config_dir}/${tenant}/${environment}"

    cat >"${config_dir}/config.yaml" <<EOF
defaulttenant: ${tenant}
EOF

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
EOF
}

initialize_codex_config() {
    codex_dir="${HOME}/.codex"
    codex_config="${codex_dir}/config.toml"
    mcp_url="http://127.0.0.1:${ERUN_MCP_PORT:-17000}${ERUN_MCP_PATH:-/mcp}"

    mkdir -p "${codex_dir}"
    touch "${codex_config}"

    tmp_config="${codex_config}.tmp"
    awk '
        /^\[mcp_servers\.erun\]$/ { skip = 1; next }
        /^\[/ && skip { skip = 0 }
        !skip { print }
    ' "${codex_config}" >"${tmp_config}"
    mv "${tmp_config}" "${codex_config}"

    cat >>"${codex_config}" <<EOF

[mcp_servers.erun]
url = "${mcp_url}"
tool_timeout_sec = 600
EOF
}

start_sshd() {
    if ! runtime_sshd_enabled; then
        return
    fi

    sshd_dir="${HOME}/.sshd"
    host_key_dir="${sshd_dir}/host_keys"
    pid_file="${sshd_dir}/sshd.pid"
    config_file="${sshd_dir}/sshd_config"
    mkdir -p "${HOME}/.ssh" "${host_key_dir}"
    chmod 700 "${HOME}/.ssh" "${sshd_dir}" "${host_key_dir}"

    if [ -r "${pid_file}" ] && kill -0 "$(cat "${pid_file}")" 2>/dev/null; then
        return
    fi
    rm -f "${pid_file}"

    host_key="${host_key_dir}/ssh_host_ed25519_key"
    if [ ! -f "${host_key}" ]; then
        ssh-keygen -q -t ed25519 -N "" -f "${host_key}" >/dev/null 2>&1
    fi
    chmod 600 "${host_key}"
    chmod 644 "${host_key}.pub"

    cat >"${config_file}" <<EOF
Port 2222
ListenAddress 0.0.0.0
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
}

run_shell() {
    repo_dir=$(runtime_repo_dir)

    if [ -d "${repo_dir}" ]; then
        cd "${repo_dir}"
    fi

    exec /bin/bash -i
}

write_kubeconfig
start_sshd

if [ "${1:-}" = "shell" ]; then
    shift
    initialize_erun_config
    initialize_codex_config
    run_shell "$@"
fi

initialize_erun_config
initialize_codex_config

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

exec "$@"
