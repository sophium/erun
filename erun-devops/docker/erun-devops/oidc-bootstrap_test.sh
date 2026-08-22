#!/bin/sh

# Tests for oidc-bootstrap.sh: the platform's OIDC applications (erun-console,
# erun-cli) are reconciled against Zitadel's Management API on every tick, not
# only created once. What is locked here is the reconcile itself — creation,
# converging an existing app's redirect URIs to a changed configured list,
# staying idempotent when nothing changed, refusing to converge to an empty
# list, and being loud (not silent) when an update fails — using a fake
# `curl` and `kubectl` on PATH so the real Management API and cluster are
# never touched. Sibling to worktree-adopt_test.sh / session-prune_test.sh:
# a real baked script exercised as a subprocess, not sourced and mocked.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
bootstrap="${script_dir}/oidc-bootstrap.sh"
if [ ! -x "${bootstrap}" ]; then
    chmod +x "${bootstrap}"
fi

command -v jq >/dev/null 2>&1 || {
    echo "FAIL: jq is required to run this test" >&2
    exit 1
}

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t oidc-bootstrap-test)"
trap 'rm -rf "${work_root}"' EXIT INT TERM

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

# --- The fake Management API: a JSON-file-backed store, driven by a `curl`
#     stub that pattern-matches the same URL shapes call() actually issues. ---
fakebin="${work_root}/fakebin"
mkdir -p "${fakebin}"

cat >"${fakebin}/curl" <<'EOF'
#!/bin/sh
set -eu
method="GET"
url=""
data=""
while [ $# -gt 0 ]; do
    case "$1" in
        -H) shift 2; continue ;;
        -X) method="$2"; shift 2; continue ;;
        -d) data="$2"; shift 2; continue ;;
        http://*|https://*) url="$1"; shift; continue ;;
        *) shift; continue ;;
    esac
done

projects="${STATE_DIR}/projects.json"
apps="${STATE_DIR}/apps.json"

next_id() {
    n=$(cat "${STATE_DIR}/next_id" 2>/dev/null || echo 0)
    n=$((n + 1))
    echo "${n}" >"${STATE_DIR}/next_id"
    echo "id${n}"
}

after_apps="${url#*/apps/}"
app_id_from_url="${after_apps%%/*}"

case "${url}" in
    */management/v1/projects/_search)
        jq -c '{result: .}' "${projects}"
        ;;
    *management/v1/projects)
        id="$(next_id)"
        name="$(printf '%s' "${data}" | jq -r '.name')"
        jq --arg id "${id}" --arg name "${name}" '. + [{id:$id, name:$name}]' "${projects}" >"${projects}.tmp"
        mv "${projects}.tmp" "${projects}"
        printf '{"id":"%s"}' "${id}"
        ;;
    */apps/_search)
        jq -c '{result: [.[] | {id, name}]}' "${apps}"
        ;;
    */apps/oidc)
        id="$(next_id)"
        cid="cid-${id}"
        name="$(printf '%s' "${data}" | jq -r '.name')"
        cfg="$(printf '%s' "${data}" | jq --arg cid "${cid}" '. + {clientId: $cid} | del(.name)')"
        jq --arg id "${id}" --arg name "${name}" --argjson cfg "${cfg}" '. + [{id: $id, name: $name, oidcConfig: $cfg}]' "${apps}" >"${apps}.tmp"
        mv "${apps}.tmp" "${apps}"
        printf '{"clientId":"%s"}' "${cid}"
        ;;
    */oidc_config)
        if [ "${FAKE_CURL_FAIL_PUT:-0}" = "1" ]; then
            echo "fake curl: simulated PUT failure" >&2
            exit 22
        fi
        app_id="${app_id_from_url}"
        patch="${data}"
        jq --arg id "${app_id}" --argjson patch "${patch}" \
            'map(if .id == $id then .oidcConfig = (.oidcConfig + $patch) else . end)' \
            "${apps}" >"${apps}.tmp"
        mv "${apps}.tmp" "${apps}"
        echo "PUT ${app_id}" >>"${STATE_DIR}/put_log"
        printf '{}'
        ;;
    *)
        # GET a single app: .../projects/<project-id>/apps/<app-id>
        app_id="${app_id_from_url}"
        jq --arg id "${app_id}" '{app: (map(select(.id == $id)) | .[0] | {oidcConfig})}' "${apps}"
        ;;
esac
EOF
chmod +x "${fakebin}/curl"

cat >"${fakebin}/kubectl" <<'EOF'
#!/bin/sh
set -eu
case "${1:-}" in
    create) echo "kind: Stub" ;;
    apply) cat >/dev/null ;;
esac
exit 0
EOF
chmod +x "${fakebin}/kubectl"

# --- State + run helpers ---
state_dir=""
bootstrap_dir=""

reset_state() {
    state_dir="${work_root}/state"
    rm -rf "${state_dir}"
    mkdir -p "${state_dir}"
    echo '[]' >"${state_dir}/projects.json"
    echo '[]' >"${state_dir}/apps.json"
    : >"${state_dir}/put_log"

    bootstrap_dir="${work_root}/bootstrap"
    rm -rf "${bootstrap_dir}"
    mkdir -p "${bootstrap_dir}"
    echo "org-owner-pat" >"${bootstrap_dir}/admin-sa.pat"
    echo "login-client-pat" >"${bootstrap_dir}/login-client.pat"
}

# seed_project_and_app <redirects-json> <post-logout-json-or-null> — a
# pre-existing project (matching PROJECT_NAME below) with one erun-console app
# already registered, the shape a real platform carries after first init.
seed_project_and_app() {
    jq --arg name "test-platform" '. + [{id: "p1", name: $name}]' "${state_dir}/projects.json" >"${state_dir}/projects.json.tmp"
    mv "${state_dir}/projects.json.tmp" "${state_dir}/projects.json"
    logout="$2"
    if [ "${logout}" = "null" ]; then
        logout='[]'
    fi
    jq -n --argjson redirects "$1" --argjson logout "${logout}" '[{
        id: "a1",
        name: "erun-console",
        oidcConfig: {
            clientId: "cid-existing",
            redirectUris: $redirects,
            postLogoutRedirectUris: $logout,
            responseTypes: ["OIDC_RESPONSE_TYPE_CODE"],
            grantTypes: ["OIDC_GRANT_TYPE_AUTHORIZATION_CODE"],
            appType: "OIDC_APP_TYPE_USER_AGENT",
            authMethodType: "OIDC_AUTH_METHOD_TYPE_NONE",
            accessTokenType: "OIDC_TOKEN_TYPE_JWT",
            devMode: false,
            clockSkew: "0s"
        }
    }]' >"${state_dir}/apps.json"
}

console_redirects() {
    jq -c '.[] | select(.name == "erun-console") | .oidcConfig.redirectUris' "${state_dir}/apps.json"
}

console_post_logout() {
    jq -c '.[] | select(.name == "erun-console") | .oidcConfig.postLogoutRedirectUris' "${state_dir}/apps.json"
}

put_count() {
    wc -l <"${state_dir}/put_log" | tr -d ' '
}

# run_once <console-redirect-uris-json> — one reconcile tick against the fake
# Management API. Stdout+stderr land in ${work_root}/run.log.
run_once() {
    env -i \
        PATH="${fakebin}:/usr/bin:/bin" \
        STATE_DIR="${state_dir}" \
        BOOTSTRAP_DIR="${bootstrap_dir}" \
        CORE_PORT=8080 \
        EXTERNAL_DOMAIN="auth.example.test" \
        PROJECT_NAME="test-platform" \
        CONFIGMAP_NAME="test-zitadel-oidc-clients" \
        PATS_SECRET_NAME="test-zitadel-pats" \
        POD_NAMESPACE="test-ns" \
        CONSOLE_REDIRECT_URIS="$1" \
        OIDC_BOOTSTRAP_RUN_ONCE=1 \
        FAKE_CURL_FAIL_PUT="${FAKE_CURL_FAIL_PUT:-0}" \
        "${bootstrap}" >"${work_root}/run.log" 2>&1
}

# --- 1. First tick creates the console app with the configured redirect URI ---
reset_state
run_once '["https://console.example.test/"]' || fail "the first reconcile tick should succeed"
[ "$(console_redirects)" = '["https://console.example.test/"]' ] ||
    fail "a newly created app must carry the configured redirect URI"
grep -q 'oidc-bootstrap: ready' "${work_root}/run.log" ||
    fail "a successful tick must report readiness"

# --- 2. An app that already exists with a DIFFERENT redirect URI must be
#        converged to the configured one on this tick (#1135). This is the
#        exact scenario the bug report described: the configured value
#        changed after the app was first created, and nothing re-applied it. ---
reset_state
seed_project_and_app '["https://old.example.test/"]' '["https://old.example.test/"]'
run_once '["https://new.example.test/"]' || fail "a reconcile tick that converges a changed redirect must still succeed"
[ "$(console_redirects)" = '["https://new.example.test/"]' ] ||
    fail "the app's redirectUris must converge to the newly configured value, not stay on the one it was created with"
[ "$(console_post_logout)" = '["https://new.example.test/"]' ] ||
    fail "postLogoutRedirectUris must converge alongside redirectUris"
grep -q 'converged erun-console redirect URIs from .*old.example.test.* to .*new.example.test' "${work_root}/run.log" ||
    fail "a tick that changes something must say what it changed, from what to what"

# --- 3. A second app of the exact same name, in the resolved project, must
#        never be created — converging reuses the existing app ---
count_console_apps="$(jq '[.[] | select(.name == "erun-console")] | length' "${state_dir}/apps.json")"
[ "${count_console_apps}" = "1" ] || fail "convergence must update the existing app, never create a duplicate"

# --- 4. Idempotent: a tick with nothing to change performs no write and logs
#        nothing new (#1135 acceptance criteria) ---
reset_state
seed_project_and_app '["https://console.example.test/"]' '["https://console.example.test/"]'
run_once '["https://console.example.test/"]' || fail "a no-op reconcile tick must still succeed"
[ "$(put_count)" = "0" ] || fail "a tick with nothing to change must not write to the app at all"
grep -q 'converged erun-console' "${work_root}/run.log" &&
    fail "an unchanged tick must not log a convergence that did not happen"

# --- 5. Registering a SECOND console origin alongside the first is additive,
#        not a cutover (#1131) ---
reset_state
seed_project_and_app '["https://console.example.test/"]' '["https://console.example.test/"]'
run_once '["https://console.example.test/", "https://console2.example.test/"]' ||
    fail "adding a second console redirect URI must reconcile successfully"
[ "$(console_redirects | jq -cS 'sort')" = "$(printf '["https://console.example.test/","https://console2.example.test/"]' | jq -cS 'sort')" ] ||
    fail "both the original and the additional console redirect URI must be registered"

# --- 6. Removing a URI from the configured list removes it from the app
#        (#1131 acceptance criteria) ---
reset_state
seed_project_and_app '["https://console.example.test/", "https://console2.example.test/"]' '["https://console.example.test/", "https://console2.example.test/"]'
run_once '["https://console.example.test/"]' || fail "shrinking the configured list must reconcile successfully"
[ "$(console_redirects)" = '["https://console.example.test/"]' ] ||
    fail "a URI dropped from the configured list must be dropped from the app too"

# --- 7. Never converge to an empty list: unsetting every configured redirect
#        leaves the app's existing registration alone rather than wiping
#        sign-in outright (#1135 safety property) ---
reset_state
seed_project_and_app '["https://console.example.test/"]' '["https://console.example.test/"]'
run_once '[]' || fail "a tick with no configured console redirect URI must still succeed"
[ "$(console_redirects)" = '["https://console.example.test/"]' ] ||
    fail "an app must never be converged to an empty redirect list"
[ "$(put_count)" = "0" ] || fail "refusing to converge to an empty list must not write to the app at all"

# --- 8. An update failure is loud, and leaves the app exactly as it was
#        (#1135 acceptance criteria: "an update failure must be loud") ---
reset_state
seed_project_and_app '["https://old.example.test/"]' '["https://old.example.test/"]'
FAKE_CURL_FAIL_PUT=1
run_once '["https://new.example.test/"]' && fail="0" || fail="1"
FAKE_CURL_FAIL_PUT=0
[ "$(console_redirects)" = '["https://old.example.test/"]' ] ||
    fail "a failed update must leave the app's redirect URIs exactly as they were"
grep -q 'FAILED to converge erun-console' "${work_root}/run.log" ||
    fail "an update failure must be logged loudly, not swallowed in silence"

# --- 9. Every field the update does not intend to change is preserved, not
#        dropped, because the update endpoint replaces the whole config
#        (#1135 acceptance criteria) ---
reset_state
seed_project_and_app '["https://old.example.test/"]' '["https://old.example.test/"]'
run_once '["https://new.example.test/"]' || fail "reconcile must succeed"
jq -e '.[] | select(.name == "erun-console") | .oidcConfig | .accessTokenType == "OIDC_TOKEN_TYPE_JWT"' "${state_dir}/apps.json" >/dev/null ||
    fail "accessTokenType must survive a redirect-only convergence"
jq -e '.[] | select(.name == "erun-console") | .oidcConfig | .appType == "OIDC_APP_TYPE_USER_AGENT"' "${state_dir}/apps.json" >/dev/null ||
    fail "appType must survive a redirect-only convergence"
jq -e '.[] | select(.name == "erun-console") | .oidcConfig | .clockSkew == "0s"' "${state_dir}/apps.json" >/dev/null ||
    fail "fields the reconcile does not manage (e.g. clockSkew) must survive a redirect-only convergence"

echo "PASS: oidc-bootstrap reconcile"
