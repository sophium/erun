#!/bin/sh

set -eu

write_kubeconfig() {
    if [ -z "${KUBERNETES_SERVICE_HOST:-}" ]; then
        return
    fi

    token_file=/var/run/secrets/kubernetes.io/serviceaccount/token
    ca_file=/var/run/secrets/kubernetes.io/serviceaccount/ca.crt
    namespace_file=/var/run/secrets/kubernetes.io/serviceaccount/namespace

    if [ ! -r "${token_file}" ] || [ ! -r "${ca_file}" ] || [ ! -r "${namespace_file}" ]; then
        return
    fi

    namespace=$(cat "${namespace_file}")
    kube_dir="${HOME}/.kube"
    kubeconfig_path="${KUBECONFIG:-${kube_dir}/config}"
    server="https://${KUBERNETES_SERVICE_HOST}:${KUBERNETES_SERVICE_PORT_HTTPS:-443}"

    mkdir -p "${kube_dir}"

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
}

initialize_erun_config() {
    repo_dir="${ERUN_REPO_PATH:-${HOME}/git/erun}"

    if [ ! -d "${repo_dir}/.git" ]; then
        return
    fi

    cd "${repo_dir}"
    erun init -y --kubernetes-context "${ERUN_KUBERNETES_CONTEXT:-in-cluster}"
}

write_kubeconfig
initialize_erun_config

exec erun mcp "$@"
