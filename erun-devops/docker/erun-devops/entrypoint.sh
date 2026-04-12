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

initialize_erun_config() {
    repo_dir=$(runtime_repo_dir)
    tenant="${ERUN_TENANT:-}"
    environment="${ERUN_ENVIRONMENT:-}"
    config_home="${XDG_CONFIG_HOME:-${HOME}/.config}"
    config_dir="${config_home}/erun"

    if [ -z "${tenant}" ] || [ -z "${environment}" ]; then
        return
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
EOF
}

run_shell() {
    repo_dir=$(runtime_repo_dir)

    if [ -d "${repo_dir}" ]; then
        cd "${repo_dir}"
    fi

    exec /bin/bash -i
}

write_kubeconfig

if [ "${1:-}" = "shell" ]; then
    shift
    initialize_erun_config
    run_shell "$@"
fi

initialize_erun_config

exec erun mcp "$@"
