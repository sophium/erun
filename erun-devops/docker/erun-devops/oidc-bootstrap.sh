#!/bin/sh

# oidc-bootstrap idempotently reconciles the platform's OIDC applications
# (erun-console, erun-cli, erun-mobile) against Zitadel's Management API, and
# persists the two bootstrap PATs it authenticates with into a durable Secret.
#
# It runs as a sidecar, not a hook Job, because it needs the org-owner PAT
# core writes to the shared bootstrap emptyDir, which a separate Job cannot
# mount. Every call carries an explicit Host header naming the external
# domain: Zitadel resolves the instance a Management API call targets from
# that header, not from the loopback address the call is actually addressed
# to, and a call with no Host header 404s against every path.
#
# Reconciliation, not create-once: the console's configured redirect URIs are
# converged on every tick via the app's OIDC config update endpoint, not only
# applied at first creation. An app whose registered redirectUris/
# postLogoutRedirectUris already match the configured list is left alone —
# no write, no new log line. A change is logged from what to what. A failed
# update is logged loudly rather than silently leaving a stale registration
# in place, which is what let a changed console address go unnoticed until
# sign-in broke in production. Converging to an empty list is refused: an
# operator who unsets every configured redirect keeps whatever is already
# registered rather than losing sign-in outright, mirroring the ConfigMap
# publish guard below.
#
# It never publishes a ConfigMap result unless the project and the erun-cli
# app genuinely resolved — the console and mobile app ids are empty on
# purpose when no redirect URI is configured for them, but any other empty id
# aborts the reconcile before it touches the ConfigMap, so a resolution
# failure leaves prior good data alone instead of overwriting it with empty
# strings.
#
# erun-mobile is a native OIDC app (Authorization Code + PKCE, no device
# code — a mobile client always has a system browser to redirect through)
# reconciled the same way as erun-console: its redirect URI is a custom URL
# scheme owned by whichever mobile client actually ships, so it is
# chart-configurable (MOBILE_REDIRECT_URIS) rather than fixed like
# erun-cli's loopback, and optional the same way erun-console is — a
# platform with no mobile client yet can leave it unconfigured.
#
# Reconciles periodically rather than exiting, so a ConfigMap, an app, or the
# PATs Secret deleted out of band is restored without a redeploy. Set
# OIDC_BOOTSTRAP_RUN_ONCE=1 to reconcile a single tick and exit instead —
# only meant for tests driving this script directly.

set -eu

: "${CORE_PORT:?CORE_PORT is required}"
: "${EXTERNAL_DOMAIN:?EXTERNAL_DOMAIN is required}"
: "${PROJECT_NAME:?PROJECT_NAME is required}"
: "${CONFIGMAP_NAME:?CONFIGMAP_NAME is required}"
: "${PATS_SECRET_NAME:?PATS_SECRET_NAME is required}"
: "${POD_NAMESPACE:?POD_NAMESPACE is required}"
CONSOLE_REDIRECT_URIS="${CONSOLE_REDIRECT_URIS:-[]}"
MOBILE_REDIRECT_URIS="${MOBILE_REDIRECT_URIS:-[]}"
BOOTSTRAP_DIR="${BOOTSTRAP_DIR:-/zitadel/bootstrap}"

# No configured value ever drifts today, but the app is reconciled the same
# way as erun-console so the two do not diverge in shape.
CLI_REDIRECT_URIS='["http://127.0.0.1/callback", "http://localhost/callback"]'

pat_file="${BOOTSTRAP_DIR}/admin-sa.pat"
login_pat_file="${BOOTSTRAP_DIR}/login-client.pat"
base="http://localhost:${CORE_PORT}"

call() {
    curl -fsS -H "Authorization: Bearer ${pat}" -H "Host: ${EXTERNAL_DOMAIN}" -H 'Content-Type: application/json' "$@"
}

persist_pats() {
    if [ ! -s "${login_pat_file}" ]; then
        echo "oidc-bootstrap: login-client PAT not yet written, skipping persist" >&2
        return 1
    fi
    kubectl create secret generic "${PATS_SECRET_NAME}" \
        --from-file="admin-sa.pat=${pat_file}" \
        --from-file="login-client.pat=${login_pat_file}" \
        --namespace "${POD_NAMESPACE}" \
        --dry-run=client -o yaml | kubectl apply -f -
}

find_project_id() {
    call -X POST "${base}/management/v1/projects/_search" -d '{}' \
        | jq -r --arg name "${PROJECT_NAME}" '.result[]? | select(.name == $name) | .id' | head -n1
}

ensure_project() {
    id="$(find_project_id || true)"
    if [ -n "${id:-}" ]; then
        echo "$id"
        return
    fi
    call -X POST "${base}/management/v1/projects" \
        -d "$(jq -n --arg name "${PROJECT_NAME}" '{name:$name}')" | jq -r '.id'
}

find_app_id() {
    name=$1
    call -X POST "${base}/management/v1/projects/${project_id}/apps/_search" -d '{}' \
        | jq -r --arg name "$name" '.result[]? | select(.name == $name) | .id' | head -n1
}

get_app() {
    call "${base}/management/v1/projects/${project_id}/apps/$1"
}

# converge_oidc_redirects <name> <app_id> <app_json> <desired_redirects_json> <desired_logout_json|null>
#
# Compares the app's live redirectUris (and, unless desired_logout is the
# literal string "null", postLogoutRedirectUris) against the desired lists
# and, only on a difference, replaces them via the OIDC config update
# endpoint. That endpoint replaces the whole config rather than merging, so
# every field the operator did not ask to change is read back off the live
# app and carried over untouched.
converge_oidc_redirects() {
    name=$1
    app_id=$2
    app_json=$3
    desired_redirects=$4
    desired_logout=$5

    desired_redirects_sorted="$(printf '%s' "${desired_redirects}" | jq -cS 'sort')"
    current_redirects_sorted="$(printf '%s' "${app_json}" | jq -cS '(.app.oidcConfig.redirectUris // []) | sort')"

    if [ "${desired_logout}" = "null" ]; then
        current_logout_sorted="null"
        desired_logout_sorted="null"
    else
        current_logout_sorted="$(printf '%s' "${app_json}" | jq -cS '(.app.oidcConfig.postLogoutRedirectUris // []) | sort')"
        desired_logout_sorted="$(printf '%s' "${desired_logout}" | jq -cS 'sort')"
    fi

    if [ "${current_redirects_sorted}" = "${desired_redirects_sorted}" ] && [ "${current_logout_sorted}" = "${desired_logout_sorted}" ]; then
        return 0
    fi

    if [ "${desired_redirects_sorted}" = "[]" ]; then
        echo "oidc-bootstrap: ${name} has no configured redirect URI; leaving its registered ${current_redirects_sorted} untouched" >&2
        return 0
    fi

    update_body="$(printf '%s' "${app_json}" | jq -c \
        --argjson redirects "${desired_redirects}" \
        --argjson logout "${desired_logout}" \
        '.app.oidcConfig
          | .redirectUris = $redirects
          | (if $logout == null then . else .postLogoutRedirectUris = $logout end)
          | {responseTypes, grantTypes, appType, authMethodType, redirectUris, postLogoutRedirectUris, accessTokenType, devMode, accessTokenRoleAssertion, idTokenRoleAssertion, idTokenUserinfoAssertion, clockSkew, additionalOrigins, skipNativeAppSuccessPage}')"

    if call -X PUT "${base}/management/v1/projects/${project_id}/apps/${app_id}/oidc_config" -d "${update_body}" >/dev/null; then
        echo "oidc-bootstrap: converged ${name} redirect URIs from ${current_redirects_sorted} to ${desired_redirects_sorted}" >&2
        return 0
    fi
    echo "oidc-bootstrap: FAILED to converge ${name} redirect URIs (wanted ${desired_redirects_sorted}); app left unchanged" >&2
    return 1
}

ensure_console_app() {
    app_id="$(find_app_id erun-console || true)"
    if [ -z "${app_id:-}" ]; then
        if [ "${CONSOLE_REDIRECT_URIS}" = "[]" ]; then
            echo "oidc-bootstrap: no console redirect URI configured; skipping the erun-console app" >&2
            echo ""
            return
        fi
        call -X POST "${base}/management/v1/projects/${project_id}/apps/oidc" -d "$(
            jq -n --argjson redirects "${CONSOLE_REDIRECT_URIS}" '{
                name: "erun-console",
                redirectUris: $redirects,
                responseTypes: ["OIDC_RESPONSE_TYPE_CODE"],
                grantTypes: ["OIDC_GRANT_TYPE_AUTHORIZATION_CODE"],
                appType: "OIDC_APP_TYPE_USER_AGENT",
                authMethodType: "OIDC_AUTH_METHOD_TYPE_NONE",
                postLogoutRedirectUris: $redirects,
                accessTokenType: "OIDC_TOKEN_TYPE_JWT"
            }'
        )" | jq -r '.clientId'
        return
    fi

    app_json="$(get_app "${app_id}")" || {
        echo "oidc-bootstrap: FAILED to read the erun-console app; leaving it unchanged" >&2
        echo ""
        return
    }
    cid="$(printf '%s' "${app_json}" | jq -r '.app.oidcConfig.clientId')"
    converge_oidc_redirects erun-console "${app_id}" "${app_json}" "${CONSOLE_REDIRECT_URIS}" "${CONSOLE_REDIRECT_URIS}" || true
    echo "${cid}"
}

ensure_cli_app() {
    app_id="$(find_app_id erun-cli || true)"
    if [ -z "${app_id:-}" ]; then
        call -X POST "${base}/management/v1/projects/${project_id}/apps/oidc" -d "$(
            jq -n --argjson redirects "${CLI_REDIRECT_URIS}" '{
                name: "erun-cli",
                redirectUris: $redirects,
                responseTypes: ["OIDC_RESPONSE_TYPE_CODE"],
                grantTypes: ["OIDC_GRANT_TYPE_AUTHORIZATION_CODE", "OIDC_GRANT_TYPE_DEVICE_CODE"],
                appType: "OIDC_APP_TYPE_NATIVE",
                authMethodType: "OIDC_AUTH_METHOD_TYPE_NONE",
                accessTokenType: "OIDC_TOKEN_TYPE_JWT"
            }'
        )" | jq -r '.clientId'
        return
    fi

    app_json="$(get_app "${app_id}")" || {
        echo "oidc-bootstrap: FAILED to read the erun-cli app; leaving it unchanged" >&2
        echo ""
        return
    }
    cid="$(printf '%s' "${app_json}" | jq -r '.app.oidcConfig.clientId')"
    converge_oidc_redirects erun-cli "${app_id}" "${app_json}" "${CLI_REDIRECT_URIS}" null || true
    echo "${cid}"
}

ensure_mobile_app() {
    app_id="$(find_app_id erun-mobile || true)"
    if [ -z "${app_id:-}" ]; then
        if [ "${MOBILE_REDIRECT_URIS}" = "[]" ]; then
            echo "oidc-bootstrap: no mobile redirect URI configured; skipping the erun-mobile app" >&2
            echo ""
            return
        fi
        call -X POST "${base}/management/v1/projects/${project_id}/apps/oidc" -d "$(
            jq -n --argjson redirects "${MOBILE_REDIRECT_URIS}" '{
                name: "erun-mobile",
                redirectUris: $redirects,
                responseTypes: ["OIDC_RESPONSE_TYPE_CODE"],
                grantTypes: ["OIDC_GRANT_TYPE_AUTHORIZATION_CODE"],
                appType: "OIDC_APP_TYPE_NATIVE",
                authMethodType: "OIDC_AUTH_METHOD_TYPE_NONE",
                accessTokenType: "OIDC_TOKEN_TYPE_JWT"
            }'
        )" | jq -r '.clientId'
        return
    fi

    app_json="$(get_app "${app_id}")" || {
        echo "oidc-bootstrap: FAILED to read the erun-mobile app; leaving it unchanged" >&2
        echo ""
        return
    }
    cid="$(printf '%s' "${app_json}" | jq -r '.app.oidcConfig.clientId')"
    converge_oidc_redirects erun-mobile "${app_id}" "${app_json}" "${MOBILE_REDIRECT_URIS}" null || true
    echo "${cid}"
}

reconcile() {
    persist_pats || echo "oidc-bootstrap: PATs Secret not persisted this tick" >&2

    project_id="$(ensure_project || true)"
    console_client_id="$(ensure_console_app || true)"
    cli_client_id="$(ensure_cli_app || true)"
    mobile_client_id="$(ensure_mobile_app || true)"

    # The console and mobile app ids are legitimately empty when no redirect
    # URI is configured for them; the project and the CLI app are not
    # optional. Publishing on a partial resolution would overwrite good
    # client ids with empty strings on every subsequent tick, so a failed
    # resolution leaves the ConfigMap untouched instead.
    if [ -z "${project_id}" ] || [ -z "${cli_client_id}" ]; then
        echo "oidc-bootstrap: resolution incomplete (project=${project_id:-<empty>} cli=${cli_client_id:-<empty>}), leaving ${CONFIGMAP_NAME} untouched" >&2
        return 1
    fi

    kubectl create configmap "${CONFIGMAP_NAME}" \
        --from-literal=consoleClientId="${console_client_id}" \
        --from-literal=cliClientId="${cli_client_id}" \
        --from-literal=mobileClientId="${mobile_client_id}" \
        --namespace "${POD_NAMESPACE}" \
        --dry-run=client -o yaml | kubectl apply -f -
    echo "oidc-bootstrap: ready project=${project_id} console=${console_client_id} cli=${cli_client_id} mobile=${mobile_client_id}"
}

echo "oidc-bootstrap: waiting for the org-owner PAT"
until [ -s "${pat_file}" ]; do sleep 2; done
pat="$(cat "${pat_file}")"

if [ "${OIDC_BOOTSTRAP_RUN_ONCE:-0}" = "1" ]; then
    reconcile
    exit $?
fi

reconcile || echo "oidc-bootstrap: initial reconcile incomplete, retrying next tick" >&2
while true; do
    sleep 300
    reconcile || echo "oidc-bootstrap: reconcile failed, retrying next tick" >&2
done
