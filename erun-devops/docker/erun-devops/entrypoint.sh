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
EOF
        # Replicate the outer cloud-context name (e.g.
        # `erun-001-020362606330-eu-west-2`) as a second context
        # entry pointing at the same in-cluster cluster/user/namespace.
        # The desktop-synced env config, the contribute clone's
        # ~/.config/erun/<tenant>/<env>/config.yaml, and any
        # cloud-context lookup that knows the outer name all end up
        # invoking `kubectl --context <outer-name>` from inside the
        # pod. Without this alias that call returns "context does
        # not exist" and the open path fails the deployment check.
        # The alias is harmless when ERUN_CLOUD_CONTEXT_NAME is unset
        # or already equals in-cluster.
        outer_context="${ERUN_CLOUD_CONTEXT_NAME:-${ERUN_KUBERNETES_CONTEXT:-}}"
        if [ -n "${outer_context}" ] && [ "${outer_context}" != "in-cluster" ]; then
            cat >>"${kubeconfig_path}" <<EOF
  - context:
      cluster: in-cluster
      namespace: ${namespace}
      user: erun-devops
    name: ${outer_context}
EOF
        fi
        cat >>"${kubeconfig_path}" <<EOF
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

runtime_outputs_dir() {
    printf '%s\n' "${ERUN_OUTPUTS_DIR:-${HOME}/.erun/outputs}"
}

# ensure_outputs_dir creates the agent outputs directory at boot. The image
# already creates it (Dockerfile) and the chart exports ERUN_OUTPUTS_DIR, but
# older images / overridden paths may not have it, so create it here as a
# boot-time fallback. Agents and skills write deliverables there; `erun outputs`
# lists and downloads from it.
ensure_outputs_dir() {
    mkdir -p "$(runtime_outputs_dir)" 2>/dev/null || true
}

# sync_registry_credential merges the docker config `erun init` mints into a
# Secret into ~/.docker/config.json, seeding only host entries the pod
# doesn't already carry -- the same "seed what's missing, never overwrite"
# rule initialize_erun_config's config injection follows, because the pod's
# own docker login (or gh-driven push-recovery) is more current than what
# erun resolved on the operator's host at init time. A no-op when the chart
# mounted nothing (older chart, or init found no host credential to give).
# ERUN_REGISTRY_CREDENTIAL_SRC_OVERRIDE is a test seam only -- the mount path
# is a fixed contract with the chart's registry-credential volume, never an
# operator-facing knob.
sync_registry_credential() {
    src="${ERUN_REGISTRY_CREDENTIAL_SRC_OVERRIDE:-/etc/erun/registry-credential/.dockerconfigjson}"
    [ -r "${src}" ] || return 0
    command -v node >/dev/null 2>&1 || return 0

    dest="${HOME}/.docker/config.json"
    mkdir -p "$(dirname "${dest}")"
    touch "${dest}"

    ERUN_REGISTRY_CREDENTIAL_SRC="${src}" ERUN_REGISTRY_CREDENTIAL_DEST="${dest}" node <<'NODE' 2>/dev/null || true
const fs = require('fs');

const srcPath = process.env.ERUN_REGISTRY_CREDENTIAL_SRC;
const destPath = process.env.ERUN_REGISTRY_CREDENTIAL_DEST;

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

const provisioned = readJSON(srcPath);
const existing = readJSON(destPath);
existing.auths = existing.auths || {};

let changed = false;
for (const [host, entry] of Object.entries(provisioned.auths || {})) {
  if (existing.auths[host]) {
    continue;
  }
  existing.auths[host] = entry;
  changed = true;
}

if (changed) {
  fs.writeFileSync(destPath, `${JSON.stringify(existing, null, 2)}\n`, { mode: 0o600 });
}
NODE
}

# sync_platform_alias seeds the pod's own erun cloud config from the Secret
# `erun init` minted on the invoking host's signed-in erun platform alias, so
# a fresh agent environment can call the platform API -- including the merge
# queue's read and self-report routes -- without a human completing an
# interactive OIDC login inside the pod, which no unattended environment can do.
# Seeds only what the pod does not already carry, the same "never overwrite"
# rule sync_registry_credential follows: a pod that somehow holds its own alias
# keeps it. A no-op when the chart mounted nothing (older chart, or init found
# no signed-in host alias to give).
# ERUN_PLATFORM_ALIAS_SRC_OVERRIDE is a test seam only -- the mount path is a
# fixed contract with the chart's platform-alias volume, never an
# operator-facing knob.
sync_platform_alias() {
    src_dir="${ERUN_PLATFORM_ALIAS_SRC_OVERRIDE:-/etc/erun/platform-alias}"
    entry="${src_dir}/cloud-provider-entry.yaml"
    [ -r "${entry}" ] || return 0

    config_dir="${XDG_CONFIG_HOME:-${HOME}/.config}/erun"
    mkdir -p "${config_dir}"
    config_file="${config_dir}/config.yaml"

    # The alias travels as its own rendered entry rather than being re-rendered
    # from env vars: the pod has none for a platform alias (ERUN_CLOUD_PROVIDER
    # is the *infrastructure* provider), so the entry is the config the host was
    # actually signed in with. It carries no `cloudproviders:` key of its own --
    # when initialize_erun_config already emitted that key for an infrastructure
    # provider, a second one would be a duplicate mapping key, and the config
    # reader refuses the whole file rather than merging them.
    if grep -q '^cloudproviders:' "${config_file}" 2>/dev/null; then
        cat "${entry}" >>"${config_file}"
    else
        printf 'cloudproviders:\n' >>"${config_file}"
        cat "${entry}" >>"${config_file}"
    fi

    # The token file is named after a hash of its ref, which init computed with
    # the store's own function and shipped as a filename; hashing in shell here
    # would be a second implementation free to drift from the first.
    secret_file="${src_dir}/cloud-secret-file"
    secret_token="${src_dir}/cloud-secret-token"
    if [ -r "${secret_file}" ] && [ -r "${secret_token}" ]; then
        dest_dir="${config_dir}/cloud-secrets"
        dest="${dest_dir}/$(cat "${secret_file}")"
        if [ ! -f "${dest}" ]; then
            mkdir -p "${dest_dir}"
            chmod 700 "${dest_dir}" 2>/dev/null || true
            cat "${secret_token}" >"${dest}"
            chmod 600 "${dest}" 2>/dev/null || true
        fi
    fi
}

# ensure_git_safe_directory lets git operate on the worktree even when it is a
# host mount owned by a foreign uid. A local-agent env on Windows shares the repo
# into the WSL2 node, where the files surface as root:root; git's dubious-owner
# guard then aborts every in-pod `git status`, which the desktop renders as an
# empty "No changes" view. Marking the worktree safe is a no-op on a PVC worktree
# already owned by erun, so it is applied unconditionally and idempotently.
ensure_git_safe_directory() {
    command -v git >/dev/null 2>&1 || return 0
    repo_dir=$(runtime_repo_dir)
    [ -n "${repo_dir}" ] || return 0
    if ! git config --global --get-all safe.directory 2>/dev/null | grep -qxF "${repo_dir}"; then
        git config --global --add safe.directory "${repo_dir}" 2>/dev/null || true
    fi
}

runtime_repo_is_remote() {
    case "${ERUN_REPO_REMOTE:-}" in
        1|true|TRUE|True|yes|YES|on|ON)
            return 0
            ;;
    esac
    return 1
}

# ensure_runtime_source clones the env's git remote into the worktree at boot
# when a runtime env opted into a mutable source mount (the chart sets
# ERUN_REPO_URL). It clones only into an empty worktree — a populated one is
# left untouched so live patches survive pod restarts — then checks out
# ERUN_REPO_REF, the deployed release tag. Every failure is non-fatal: the pod
# still serves without mounted source. Only the main runtime container runs
# this; the MCP container shares the resulting checkout over the /home/erun PVC.
ensure_runtime_source() {
    repo_url="${ERUN_REPO_URL:-}"
    [ -n "${repo_url}" ] || return 0

    repo_dir=$(runtime_repo_dir)
    if [ -d "${repo_dir}/.git" ]; then
        return 0
    fi

    mkdir -p "$(dirname "${repo_dir}")"
    if ! git clone "${repo_url}" "${repo_dir}" >/dev/null 2>&1; then
        echo "erun: failed to clone runtime source from ${repo_url}; continuing without mounted source" >&2
        return 0
    fi

    repo_ref="${ERUN_REPO_REF:-}"
    if [ -n "${repo_ref}" ]; then
        ( cd "${repo_dir}" && git checkout -q "${repo_ref}" ) >/dev/null 2>&1 \
            || echo "erun: could not check out ${repo_ref}; runtime source left at the default branch" >&2
    fi
}

# link_runtime_release surfaces the image-baked /opt/erun/release tree through
# the git folder on a sourceless runtime env, which has no worktree of its own.
# A tenant devops image bakes release artifacts there (outside /home/erun, so the
# home PVC can't shadow them); the symlink lets them show through the repo dir.
# Agent envs and mount-source runtime envs carry a real worktree, so they are
# skipped and their source is left untouched.
link_runtime_release() {
    if [ "${ERUN_ENV_TYPE:-}" != "runtime" ]; then
        return 0
    fi
    if runtime_repo_is_remote; then
        return 0
    fi
    erun-link-release "$(runtime_repo_dir)" /opt/erun/release || true
}

# runtime_session_dir is where `erun open` keeps the desktop's persistent dtach
# sockets. Container-local on purpose (see session-prune.sh). The name deliberately
# shares nothing with the desktop binary's process name: a `pkill -f` aimed at that
# binary would otherwise match every session's dtach command line.
runtime_session_dir() {
    printf '%s\n' "${ERUN_APP_SESSION_DIR:-/tmp/erun-sessions}"
}

# prune_stale_app_sessions reconciles the session directory at container start.
# A dtach server cannot outlive its container, so any socket still present is a
# leftover the desktop would otherwise read as a running session. Only the
# container-boot paths call this — never the `shell` path, which runs inside a
# live container where the sockets are real.
prune_stale_app_sessions() {
    erun-prune-sessions "$(runtime_session_dir)" 2>/dev/null || true
}

# start_cache_trim bounds the go build cache for as long as the container lives.
# Nothing else in this pod caps it: it lives under ~/.cache on the home volume,
# the volume's declared size is not a quota on a node-local storage class, and
# the go command's own trim is by age with no ceiling. An environment that keeps
# building grows it into tens of gigabytes and, sharing a node with the other
# environments doing the same, is what puts that node under DiskPressure.
#
# It runs in the background rather than at a blocking point because the first
# pass is the migration: an environment that already holds tens of gigabytes is
# brought under the cap by it, and deleting that much is not something to hold
# the rest of the boot behind. It repeats rather than running once because a
# container outlives the boot that started it -- without the loop, a long-lived
# pod would simply grow back to the size this exists to bound.
#
# The cap travels from the chart (ERUN_GO_BUILD_CACHE_MAX_BYTES), for the reason
# the docker volume's does: the pod cannot read the home claim's size off the
# mounted filesystem. A cap of 0, or none rendered at all by an older chart, is
# read as "no bound" rather than as a bound of zero, which would clear the cache
# and make every build cold.
start_cache_trim() {
    cache_dir="${GOCACHE:-${HOME}/.cache/go-build}"
    max_bytes="${ERUN_GO_BUILD_CACHE_MAX_BYTES:-17179869184}"
    interval="${ERUN_CACHE_TRIM_INTERVAL_SECONDS:-1800}"

    case "${max_bytes}" in
        '' | *[!0-9]*) return 0 ;;
    esac
    [ "${max_bytes}" -gt 0 ] || return 0
    case "${interval}" in
        '' | *[!0-9]*) interval=1800 ;;
    esac
    [ "${interval}" -gt 0 ] || return 0

    (
        while :; do
            erun-trim-cache "${cache_dir}" "${max_bytes}" || true
            sleep "${interval}"
        done
    ) &
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

    AWS_MAX_ATTEMPTS=5 AWS_RETRY_MODE=standard \
        aws --cli-connect-timeout 5 --cli-read-timeout 20 ec2 stop-instances --region "${region}" --instance-ids "${instance_id}" >/dev/null
}

graceful_quit_clients() {
    # claude-real and codex-real are Node processes spawned by the wrappers in
    # /usr/local/bin/{claude,codex}. Match against the full command line because
    # Node may rewrite argv[0]; also match the npm package paths as a fallback
    # when the launcher is a shebang script that exec's node with the cli.js.
    for pattern in 'claude-real' 'codex-real' '@anthropic-ai/claude-code' '@openai/codex'; do
        pkill -TERM -f "${pattern}" >/dev/null 2>&1 || true
    done

    deadline=$(( $(date +%s) + 20 ))
    while [ "$(date +%s)" -lt "${deadline}" ]; do
        any_running=0
        for pattern in 'claude-real' 'codex-real' '@anthropic-ai/claude-code' '@openai/codex'; do
            if pgrep -f "${pattern}" >/dev/null 2>&1; then
                any_running=1
                break
            fi
        done
        if [ "${any_running}" -eq 0 ]; then
            break
        fi
        sleep 1
    done

    sync
}

runtime_sshd_enabled() {
    case "${ERUN_SSHD_ENABLED:-}" in
        1|true|TRUE|True|yes|YES|on|ON)
            return 0
            ;;
    esac
    return 1
}

# runtime_mcp_enabled reports whether this container serves the environment's MCP
# edge. The chart sets ERUN_MCP_ENABLED explicitly so an operator can turn the
# edge off; a chart that predates it leaves the variable unset, and a configured
# port is then enough to keep serving an existing environment.
runtime_mcp_enabled() {
    case "${ERUN_MCP_ENABLED:-}" in
        1|true|TRUE|True|yes|YES|on|ON)
            return 0
            ;;
        0|false|FALSE|False|no|NO|off|OFF)
            return 1
            ;;
    esac
    [ -n "${ERUN_MCP_PORT:-}" ]
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
    env_type_line=""
    env_managed_cloud_line=""
    env_cloud_provider_alias_line=""

    if [ -z "${tenant}" ] || [ -z "${environment}" ]; then
        return
    fi
    if [ -n "${cloud_provider}" ]; then
        cloud_region=$(runtime_cloud_region)
        cloud_instance_id=$(runtime_cloud_instance_id)
    fi

    if [ -n "${ERUN_ENV_TYPE:-}" ]; then
        env_type_line="type: ${ERUN_ENV_TYPE}"
    elif runtime_repo_is_remote; then
        # Fallback for charts that predate ERUN_ENV_TYPE: emit the legacy
        # remote flag, which erun migrates to a concrete type on read.
        env_type_line="remote: true"
    fi
    if [ -n "${cloud_provider_alias}" ]; then
        env_cloud_provider_alias_line="cloudprovideralias: ${cloud_provider_alias}"
    fi
    if runtime_cloud_environment || { runtime_repo_is_remote && [ -n "${cloud_provider}" ] && [ -n "${cloud_provider_alias}" ] && [ -n "${cloud_region}" ]; }; then
        env_managed_cloud_line="managedcloud: true"
    fi

    # In-pod config injection: the build/deploy-relevant fields the pod
    # acts on, written only when the chart injected them so an older chart
    # produces a valid (thinner) config.
    env_runtime_registry_line=""
    if [ -n "${ERUN_RUNTIME_REGISTRY:-}" ]; then
        env_runtime_registry_line="runtimeregistry: ${ERUN_RUNTIME_REGISTRY}"
    fi
    env_container_registries_line=""
    if [ -n "${ERUN_CONTAINER_REGISTRIES:-}" ]; then
        # JSON is valid YAML flow style; erun decodes it straight into
        # EnvConfig.ContainerRegistries (no jq/yq needed).
        env_container_registries_line="containerregistries: ${ERUN_CONTAINER_REGISTRIES}"
    fi
    # Write the actual boolean (true *and* false) whenever the chart injected
    # it; skip only when the var is unset (older chart). A truthiness-gated
    # write would leave a stale value that `erun doctor --sync-config` could
    # never reconcile back to false.
    env_disable_build_script_line=""
    if [ -n "${ERUN_DISABLE_BUILD_SCRIPT:-}" ]; then
        env_disable_build_script_line="disablebuildscript: ${ERUN_DISABLE_BUILD_SCRIPT}"
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
name: ${tenant}
defaultenvironment: ${environment}
EOF

    cat >"${config_dir}/${tenant}/${environment}/config.yaml" <<EOF
name: ${environment}
repopath: ${repo_dir}
kubernetescontext: ${ERUN_KUBERNETES_CONTEXT:-in-cluster}
${env_type_line}
${env_cloud_provider_alias_line}
${env_managed_cloud_line}
${env_runtime_registry_line}
${env_container_registries_line}
${env_disable_build_script_line}
idle:
  timeout: ${ERUN_IDLE_TIMEOUT:-5m0s}
  workinghours: ${ERUN_IDLE_WORKING_HOURS:-08:00-20:00}
  timezone: ${ERUN_IDLE_TIMEZONE:-}
  idletrafficbytes: ${ERUN_IDLE_TRAFFIC_BYTES:-0}
EOF

    # Last, because the root config.yaml above is rewritten wholesale each boot:
    # seeding before it would be discarded on every restart.
    sync_platform_alias
}

# Reconciling the agent MCP configuration is pod-lifecycle work, not per-shell
# work: it rewrites ~/.claude and ~/.codex from the image's baked skills/agents
# and the container's env, none of which change while the container lives. The
# claim marker therefore lives in a container-lifetime directory (the same /tmp
# reasoning the desktop session dir uses), so a rebuilt image or a restarted pod
# always reconciles again, and every shell after the first in a given container
# pays nothing. `mkdir` is the claim because it is atomic: a burst of execs
# racing the entrypoint's own boot run elects exactly one runner rather than
# each paying the cost.
agent_config_state_dir() {
    printf '%s\n' "${ERUN_AGENT_CONFIG_STATE_DIR:-/tmp/erun-agent-config}"
}

run_agent_config_once() {
    agent_config_script="${1}"
    agent_config_name="${2}"

    [ -x "${agent_config_script}" ] || return 0

    agent_config_dir="$(agent_config_state_dir)"
    mkdir -p "${agent_config_dir}" 2>/dev/null || return 0
    mkdir "${agent_config_dir}/${agent_config_name}" 2>/dev/null || return 0

    "${agent_config_script}" >/dev/null 2>&1 || true
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

codex_instructions="${codex_dir}/instructions.md"
codex_existing=""
if [ -f "${codex_instructions}" ]; then
    codex_existing="$(awk '/<!-- erun-agents-md-hook -->/{skip=1} skip&&/<!-- \/erun-agents-md-hook -->/{skip=0;next} !skip{print}' "${codex_instructions}")"
fi
codex_tmp="${codex_instructions}.tmp.$$"
{
    if [ -n "${codex_existing}" ]; then
        printf '%s\n\n' "${codex_existing}"
    fi
    printf '<!-- erun-agents-md-hook -->\n# Agent Instructions\n\nIMPORTANT: Before doing anything else, read `AGENTS.md` in the project root. This is mandatory — do not skip it.\nAlso read `AGENTS.md` in any subdirectory relevant to the task at hand,\nas subdirectories may contain more specific guidance.\n\nWhen the project'\''s structure is explicit — AGENTS.md, documented module boundaries, a named file or function — read the source directly instead of spawning sub-agent searches to rediscover it. Answer the operator'\''s questions before acting; a question is not authorization to begin the work it hints at.\n<!-- /erun-agents-md-hook -->\n'
} > "${codex_tmp}" && mv "${codex_tmp}" "${codex_instructions}"

# Install baked skills, refreshing an unmodified one when the image's copy
# changed; in-pod edits are preserved (see /usr/local/bin/erun-install-skills).
erun-install-skills /etc/erun/skills "${codex_dir}/skills"

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
    run_agent_config_once "${codex_configure}" codex-mcp
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
    token_status=0
    token=$(curl -fsS --connect-timeout 1 -m 2 -X PUT "http://169.254.169.254/latest/api/token" \
        -H "X-aws-ec2-metadata-token-ttl-seconds: 60" 2>/dev/null) || token_status=$?
    if [ -n "${token}" ]; then
        curl -fsS --connect-timeout 1 -m 2 -H "X-aws-ec2-metadata-token: ${token}" "http://169.254.169.254/latest/${path}" 2>/dev/null || true
        return
    fi
    # Nothing answers the link-local address at all where IMDS is unreachable,
    # so the probe drains its whole timeout (curl 28) rather than being refused.
    # Only an IMDSv1-only instance rejects the token and can still serve the
    # plain read, and it rejects it immediately -- so a timed-out probe means
    # the fallback would buy a second timeout for an answer that cannot come.
    if [ "${token_status}" = "28" ]; then
        return
    fi
    curl -fsS --connect-timeout 1 -m 2 "http://169.254.169.254/latest/${path}" 2>/dev/null || true
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

claude_md="${claude_dir}/CLAUDE.md"
claude_existing=""
if [ -f "${claude_md}" ]; then
    claude_existing="$(awk '/<!-- erun-agents-md-hook -->/{skip=1} skip&&/<!-- \/erun-agents-md-hook -->/{skip=0;next} !skip{print}' "${claude_md}")"
fi
claude_md_tmp="${claude_md}.tmp.$$"
{
    if [ -n "${claude_existing}" ]; then
        printf '%s\n\n' "${claude_existing}"
    fi
    printf '<!-- erun-agents-md-hook -->\n# Agent Instructions\n\nIMPORTANT: Before doing anything else, read `AGENTS.md` in the project root. This is mandatory — do not skip it.\nAlso read `AGENTS.md` in any subdirectory relevant to the task at hand,\nas subdirectories may contain more specific guidance.\n\nWhen the project'\''s structure is explicit — AGENTS.md, documented module boundaries, a named file or function — read the source directly instead of spawning sub-agent searches to rediscover it. Answer the operator'\''s questions before acting; a question is not authorization to begin the work it hints at.\n<!-- /erun-agents-md-hook -->\n'
} > "${claude_md_tmp}" && mv "${claude_md_tmp}" "${claude_md}"

# Install baked skills, refreshing an unmodified one when the image's copy
# changed; in-pod edits are preserved (see /usr/local/bin/erun-install-skills).
erun-install-skills /etc/erun/skills "${claude_dir}/skills"
# Same install/refresh/preserve policy for reusable agents (single files
# instead of directories; see /usr/local/bin/erun-install-agents). Codex has
# no reusable-agent equivalent yet, so this only runs for Claude.
erun-install-agents /etc/erun/agents "${claude_dir}/agents"

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

// A gateway routes Claude the way Bedrock does -- through a provider the
// environment's config selects, rather than a claude.ai login -- so the same
// settings relay applies to both. The chart emits the base URL only when a
// gateway is configured, which makes the pod's own environment the signal and
// needs no separate flag. Only non-secret routing values are relayed: the
// credential stays in the pod environment via its Secret reference.
const configureGateway = (process.env.ANTHROPIC_BASE_URL || '').trim() !== '';
const configureRelay = configureBedrock || configureGateway;

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

// setBoolEnv writes name=1 only when the value is an enabled flag; otherwise it
// removes any existing entry. Claude Code treats CLAUDE_CODE_USE_BEDROCK /
// CLAUDE_CODE_USE_MANTLE as present-or-absent, not 1/0, so writing "0" would
// *enable* them — the variable must be omitted (and any stale "0" deleted) to
// disable.
function setBoolEnv(settings, name) {
  const normalized = envValue(name).toLowerCase();
  if (normalized === '1' || normalized === 'true' || normalized === 'yes' || normalized === 'on') {
    settings.env[name] = '1';
  } else {
    delete settings.env[name];
  }
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

if (configureRelay) {
  const settings = readJSON(settingsPath);
  settings.$schema = settings.$schema || 'https://json.schemastore.org/claude-code-settings.json';
  settings.env = ensureObject(settings, 'env');

  setBoolEnv(settings, 'CLAUDE_CODE_USE_BEDROCK');
  setBoolEnv(settings, 'CLAUDE_CODE_USE_MANTLE');
  setEnv(settings, 'AWS_REGION', region);
  setEnv(settings, 'ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION', envValue('ANTHROPIC_SMALL_FAST_MODEL_AWS_REGION', region));
  setEnv(settings, 'CLAUDE_CODE_MAX_OUTPUT_TOKENS', envValue('CLAUDE_CODE_MAX_OUTPUT_TOKENS', '32000'));
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
    // A gateway serves model ids Claude Code cannot size on its own, so the
    // window the chart declares for the catalog's default travels with it.
    'CLAUDE_CODE_MAX_CONTEXT_TOKENS',
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

// The AI tool's own turn-boundary hooks are the writer the structured
// AI-session status model was missing: without them the model resolves from
// events nothing ever reports, so "awaiting-input" -- finished a turn, waiting
// on the human, and producing no output while it waits -- is unreachable by
// anything. Each row is [the tool's hook event, the model event it reports];
// erun-common's AISessionHookBindings is the definition this table mirrors, and
// a test there reads this table back out of this file so editing one side
// alone fails rather than silently splitting the two.
//
// Busy is reported on the tool calls as well as the turn's start, because a
// report written once at the start of a turn is stale for every minute of that
// turn after it. Awaiting-input comes from the two events that mean control
// went back to the human. SessionStart is deliberately absent: it would have to
// report busy for a session nobody has prompted yet. So is SubagentStop, which
// is a subagent's boundary rather than the session's.
const aiSessionHooks = [
  ['UserPromptSubmit', 'turn-start'],
  ['PreToolUse', 'tool-use'],
  ['PostToolUse', 'tool-use'],
  ['Stop', 'turn-end'],
  ['Notification', 'notify'],
  ['SessionEnd', 'exit'],
];

// aiSessionHookCommand is one installed report. The environment check is in the
// shell rather than left to the verb so a session with no environment in scope
// forks nothing on every turn boundary -- this settings file is the operator's,
// and a Claude they ran themselves has no erun environment to report against.
// Nothing it prints or exits with can reach the turn either way: a hook runs in
// the operator's own session, and the report is state it reads, not a gate.
function aiSessionHookCommand(modelEvent) {
  return `if [ -n "\${ERUN_TENANT}" ] && [ -n "\${ERUN_ENVIRONMENT}" ]; then erun activity ai-session hook --event ${modelEvent} --tool claude >/dev/null 2>&1 || true; fi`;
}

// isAISessionHookBlock reports whether a settings hook block is the report this
// script writes, so a second boot replaces its own previous block instead of
// stacking another copy beside it, and never claims an operator's own hook that
// happens to sit on the same event.
function isAISessionHookBlock(block) {
  if (!block || typeof block !== 'object' || !Array.isArray(block.hooks)) {
    return false;
  }
  return block.hooks.some((entry) => entry && typeof entry.command === 'string' && entry.command.includes('activity ai-session hook'));
}

{
  const settings = readJSON(settingsPath);
  settings.$schema = settings.$schema || 'https://json.schemastore.org/claude-code-settings.json';
  const permissions = ensureObject(settings, 'permissions');
  permissions.defaultMode = 'bypassPermissions';
  settings.skipDangerousModePermissionPrompt = true;
  const hooks = ensureObject(settings, 'hooks');
  for (const [toolEvent, modelEvent] of aiSessionHooks) {
    const current = Array.isArray(hooks[toolEvent]) ? hooks[toolEvent] : [];
    hooks[toolEvent] = current
      .filter((block) => !isAISessionHookBlock(block))
      .concat([{ hooks: [{ type: 'command', command: aiSessionHookCommand(modelEvent) }] }]);
  }
  writeJSON(settingsPath, settings);
}
NODE
    chmod 600 "${claude_settings}" >/dev/null 2>&1 || true
    chmod 600 "${claude_state}" >/dev/null 2>&1 || true
CLAUDE_CONFIG_SCRIPT
    chmod 700 "${claude_configure}"
    run_agent_config_once "${claude_configure}" claude-code
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
# Signal a dark terminal so libraries that respect COLORFGBG skip OSC 11
# (background-color) queries, which would otherwise leak their reply into
# the shell's stdin via the Wails+PTY reply path.
export COLORFGBG='15;0'

# The entrypoint reconciles the agent MCP configuration once per container boot
# and records the claim in a container-lifetime directory; this is the same
# claim, so a shell only pays for it when it is genuinely the first in this
# container (an exec racing the entrypoint's own boot run). Every ordinary shell
# start -- including the non-interactive `sh -lc` every remote exec goes through
# -- reads two directory entries instead of re-running both configure scripts.
__erun_configure_agents_once() {
    __erun_state_dir="${ERUN_AGENT_CONFIG_STATE_DIR:-/tmp/erun-agent-config}"
    mkdir -p "${__erun_state_dir}" 2>/dev/null || return 0
    for __erun_agent in codex-mcp claude-code; do
        __erun_script="${HOME}/.erun/configure-${__erun_agent}.sh"
        if [ -x "${__erun_script}" ] && mkdir "${__erun_state_dir}/${__erun_agent}" 2>/dev/null; then
            "${__erun_script}" >/dev/null 2>&1 || true
        fi
    done
    unset __erun_state_dir __erun_agent __erun_script
    return 0
}
__erun_configure_agents_once
unset -f __erun_configure_agents_once

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

normalize_ssh_key_permissions() {
    ssh_dir="${HOME}/.ssh"
    [ -d "${ssh_dir}" ] || return 0
    # The runtime pod no longer carries a fsGroup, but every environment that
    # ran with one still holds the g+rw the kubelet ORed into each PVC file on
    # pod start: a private key that init left at 0600 is on disk as 0660 (and
    # any file that ever picked up the user-x bit as 0760). ssh refuses to use
    # a private key whose perms are looser than 0600, so re-apply the canonical
    # modes before anything tries to read them.
    # *.pub files stay world-readable; everything else in ~/.ssh is treated
    # as private — private keys, config, known_hosts, authorized_keys.
    chmod 700 "${ssh_dir}" 2>/dev/null || true
    find "${ssh_dir}" -mindepth 1 -maxdepth 1 -type f -name '*.pub' -exec chmod 644 {} + 2>/dev/null || true
    find "${ssh_dir}" -mindepth 1 -maxdepth 1 -type f ! -name '*.pub' -exec chmod 600 {} + 2>/dev/null || true
}

# pid_running reports whether PID file $1 holds a live process whose
# /proc/<pid>/cmdline matches $2. The sshd and ssh-proxy PID files live on the
# persistent /home/erun PVC and survive pod restarts, but each pod gets a fresh
# PID namespace that recycles low PIDs — so a stale PID written by a previous
# pod can collide with an unrelated live process in this one (a previous pod's
# ssh-proxy PID matching this pod's sshd, for example). A bare `kill -0` would
# then report the process as running and the caller would skip (re)starting it,
# leaving nothing listening on the SSH port. Matching the command line closes
# that gap. Distinct `_`-prefixed locals avoid clobbering the caller's globals.
pid_running() {
    _pidfile="$1"
    _pat="$2"
    [ -r "${_pidfile}" ] || return 1
    _pid=$(cat "${_pidfile}" 2>/dev/null) || return 1
    [ -n "${_pid}" ] || return 1
    kill -0 "${_pid}" 2>/dev/null || return 1
    tr '\0' ' ' < "/proc/${_pid}/cmdline" 2>/dev/null | grep -q "${_pat}"
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

    if ! pid_running "${pid_file}" "sshd"; then
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

    if pid_running "${proxy_pid_file}" "ssh-proxy"; then
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

# start_environment_monitor runs one loop per pod. Every tick samples the
# processes actually working in this container, so a build or agent run nobody
# instrumented still registers as activity instead of reading as idle; the
# auto-stop decision rides the same tick but only for a cloud-managed env, which
# is the only kind that has a host to stop.
start_environment_monitor() {
    if [ -z "${ERUN_TENANT:-}" ] || [ -z "${ERUN_ENVIRONMENT:-}" ]; then
        return
    fi

    (
        stop_log_dir="${HOME}/.erun/${ERUN_TENANT}/${ERUN_ENVIRONMENT}"
        monitor_log="${stop_log_dir}/idle-monitor.log"
        stop_log="${stop_log_dir}/idle-stop.log"
        mkdir -p "${stop_log_dir}"
        # idle-stop.log lives on the shared home PVC and survives pod and
        # host restarts. Clear it on monitor start so the desktop never
        # surfaces a stop error attributable to a previous pod lifetime.
        : >"${stop_log}"
        while :; do
            # Sampled before the sleep so the very first tick establishes the
            # CPU baseline the next one compares against; work that started
            # during boot is then visible one tick later, not two.
            erun activity sample --tenant "${ERUN_TENANT}" --environment "${ERUN_ENVIRONMENT}" >/dev/null 2>&1 || true
            sleep 30
            if ! runtime_cloud_environment; then
                continue
            fi
            tick_ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
            # The stop-ready command exits non-zero on every active env. Capture
            # the substitution in an `if` so dash's `set -e` (active script-wide)
            # does not kill this subshell on the first tick before we ever write
            # the heartbeat line.
            if check_json=$(erun activity stop-ready --json --tenant "${ERUN_TENANT}" --environment "${ERUN_ENVIRONMENT}" 2>/dev/null); then
                exit_code=0
            else
                exit_code=$?
            fi
            printf '{"ts":"%s","exit":%d,"check":%s}\n' "${tick_ts}" "${exit_code}" "${check_json:-null}" >>"${monitor_log}"
            if [ "${exit_code}" -eq 0 ]; then
                # Reset the log each attempt so it reflects only the latest
                # one — empty on success, the most recent error on failure.
                : >"${stop_log}"
                # Ask AWS to stop the host first. EC2 stop-instances returns
                # immediately with `PendingState=stopping`; the OS-level
                # shutdown follows asynchronously, so there is plenty of
                # window to gracefully terminate clients before power-off.
                # Quitting clients pre-emptively would destroy the user's
                # claude/codex state for nothing when the stop is refused
                # (e.g. `disableApiStop=true`, missing permission), and
                # would re-kill them on every subsequent tick.
                if stop_cloud_host >>"${stop_log}" 2>&1; then
                    # Persist the audit record before we hand off to
                    # graceful_quit_clients. We pipe the captured
                    # stop-ready JSON into `record-stop --state-stdin`
                    # because the Fire branch of `stop-ready` cleared
                    # stop-pending.json for crash-safety, so the
                    # in-memory state in `check_json` is the only
                    # surviving source of markers/reason/grace/policy.
                    # Best-effort — a failure here does not block the
                    # EC2 stop because the AWS call has already
                    # succeeded.
                    printf '%s\n' "${check_json}" | erun activity record-stop \
                        --tenant "${ERUN_TENANT}" \
                        --environment "${ERUN_ENVIRONMENT}" \
                        --source pod-monitor \
                        --state-stdin \
                        >>"${stop_log}" 2>&1 || true
                    graceful_quit_clients >>"${stop_log}" 2>&1 || true
                    exit 0
                fi
                # A transient AWS failure (e.g. RequestExpired) or a
                # permanent one (e.g. stop-protection) leaves the loop
                # running and the user's processes untouched; the next
                # tick re-checks stop-ready and retries. The error stays
                # in idle-stop.log for the desktop to surface until the
                # next attempt overwrites it.
            fi
        done
    ) &
}

# exec_runtime_mcp replaces the current process with the environment's MCP
# server. Both MCP paths go through it — the standalone `mcp` command and the
# supervisor the runtime container starts — so the edge is served with identical
# flags either way. A caller that wants the server supervised instead of
# replacing itself wraps the call in a subshell.
exec_runtime_mcp() {
    set -- emcp "$@" \
        --host "${ERUN_MCP_HOST:-0.0.0.0}" \
        --port "${ERUN_MCP_PORT:-17000}" \
        --path "${ERUN_MCP_PATH:-/mcp}" \
        --metrics-host "${ERUN_METRICS_HOST:-0.0.0.0}" \
        --metrics-port "${ERUN_METRICS_PORT:-9100}" \
        --metrics-enabled="${ERUN_METRICS_ENABLED:-true}" \
        --tenant "${ERUN_TENANT:-}" \
        --environment "${ERUN_ENVIRONMENT:-}" \
        --repo-path "$(runtime_repo_dir)" \
        --kubernetes-context "${ERUN_KUBERNETES_CONTEXT:-in-cluster}"

    namespace=$(runtime_namespace)
    if [ -n "${namespace}" ]; then
        set -- "$@" --namespace "${namespace}"
    fi

    echo "starting erun MCP on ${ERUN_MCP_HOST:-0.0.0.0}:${ERUN_MCP_PORT:-17000}${ERUN_MCP_PATH:-/mcp}, metrics on ${ERUN_METRICS_HOST:-0.0.0.0}:${ERUN_METRICS_PORT:-9100} (enabled=${ERUN_METRICS_ENABLED:-true})"
    exec "$@"
}

# start_runtime_mcp serves the environment's MCP edge from the runtime container
# itself, so every MCP-driven command runs with the toolchain the environment is
# built with. Nothing else supervises it here — the runtime container's own
# foreground work is an idle sleep — so the loop restarts a crashed server and
# logs each restart, keeping a crash-loop visible in the container log.
start_runtime_mcp() {
    if ! runtime_mcp_enabled; then
        return
    fi

    (
        attempt=1
        while :; do
            ( exec_runtime_mcp ) || true
            attempt=$((attempt + 1))
            echo "erun MCP exited; restarting (attempt ${attempt})"
            sleep 2
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
normalize_ssh_key_permissions
ensure_outputs_dir
sync_registry_credential
ensure_git_safe_directory
start_sshd
start_environment_monitor

if [ "${1:-}" = "shell" ]; then
    shift
    ensure_runtime_source
    link_runtime_release
    initialize_erun_config
    initialize_codex_config
    initialize_claude_config
    record_activity cli
    run_shell "$@"
fi

if [ "${1:-}" = "mcp" ]; then
    shift
    initialize_erun_config
    initialize_codex_config
    initialize_claude_config
    record_activity mcp
    exec_runtime_mcp "$@"
fi

if [ "${1:-}" = "devops" ] || [ "$#" -eq 0 ]; then
    prune_stale_app_sessions
    # Started before the rest of the boot so an environment already over its cap
    # begins coming back under it immediately; it blocks nothing.
    start_cache_trim
    ensure_runtime_source
    link_runtime_release
    initialize_erun_config
    initialize_codex_config
    initialize_claude_config
    record_activity devops
    # Started only after the worktree is prepared, so the edge can never serve a
    # half-initialised environment.
    start_runtime_mcp
    exec sleep infinity
fi

exec "$@"
