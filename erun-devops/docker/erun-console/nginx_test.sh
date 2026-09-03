#!/bin/sh

# End-to-end proof for erun#2064: the console's nginx config must not resolve
# every unmatched path to a 200 SPA shell. Runs the real, pinned
# nginx:1.27.4-alpine3.21 base with the actual default.conf.template shipped
# in this directory (rendered by the base image's own envsubst-on-templates
# entrypoint, exactly as erun-devops/k8s/erun-console/templates/console.yaml
# configures it), against a synthetic static tree standing in for a Vite
# `dist/` build. Locks three properties directly against a real running nginx,
# not a config-syntax check:
#
#   1. A missing content-hashed asset under /assets/ 404s -- it must never
#      fall back to the SPA shell, which is what let a stale cached
#      index.html's script tag fail with a MIME-type error instead of a clean
#      404 after a deploy rotated the asset hash.
#   2. An existing asset and a real app route both still resolve correctly
#      (an existing asset serves its real content; an unmatched app route
#      falls back to index.html) -- the fix must not regress ordinary SPA
#      serving to get (1).
#   3. /healthz and /version.json serve their own content and are not
#      swallowed by the SPA catch-all either.
#
# Lives beside the Dockerfile/template rather than in erun-integration: it
# needs a real docker daemon to observe actual nginx `location`/`try_files`
# behavior, which the erun-devops image test stage's bare `docker build` RUN
# step cannot provide (no nested daemon). Run this by hand, or via `erun exec
# job` in an agent env (which does carry docker), before merging a change to
# default.conf.template or the console Dockerfile's static-serving stage.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"

command -v docker >/dev/null 2>&1 || {
    echo "FAIL: docker is required" >&2
    exit 1
}

container="erun-console-nginx-test-$$"
port=18080
workdir="$(mktemp -d)"

cleanup() {
    docker rm -f "${container}" >/dev/null 2>&1 || true
    rm -rf "${workdir}"
}
trap cleanup EXIT INT TERM

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

mkdir -p "${workdir}/html/assets"
printf 'SPA-SHELL\n' >"${workdir}/html/index.html"
printf '// real asset\n' >"${workdir}/html/assets/real.js"
printf '{"version":"test-1.2.3"}\n' >"${workdir}/html/version.json"

# Render the real template ourselves (the same three substitutions the base
# image's own docker-entrypoint.d/20-envsubst-on-templates.sh performs) rather
# than relying on that entrypoint at container start, so the config can be
# `docker cp`'d in before nginx ever starts.
sed -e "s/\${NGINX_PORT}/8080/g" \
    -e "s/\${API_PROXY_HOST}/127.0.0.1/g" \
    -e "s/\${API_PROXY_PORT}/17033/g" \
    "${script_dir}/default.conf.template" >"${workdir}/default.conf"

# docker cp, not a bind mount (`-v`): the daemon this script talks to may be a
# separate dind sidecar with no shared filesystem with this script's own host
# path (the case inside an erun agent pod), so a `-v` mount silently produces
# an empty directory in the container instead of failing loudly. The
# entrypoint is overridden to `sleep infinity` so the container stays up while
# the config and static tree are copied in before nginx itself starts.
docker create --name "${container}" \
    --entrypoint sleep \
    -p "${port}:8080" \
    nginx:1.27.4-alpine3.21 infinity >/dev/null
docker start "${container}" >/dev/null
docker cp "${workdir}/default.conf" "${container}:/etc/nginx/conf.d/default.conf"
docker cp "${workdir}/html/." "${container}:/usr/share/nginx/html"
docker exec "${container}" nginx >/dev/null 2>&1

base="http://127.0.0.1:${port}"

wait_for_ready() {
    i=0
    while [ "$i" -lt 30 ]; do
        status="$(curl -s -o /dev/null -w '%{http_code}' "${base}/healthz" 2>/dev/null || true)"
        [ "${status}" = "200" ] && return 0
        i=$((i + 1))
        sleep 1
    done
    return 1
}

assert_status() {
    path="$1"
    expected="$2"
    got="$(curl -s -o /dev/null -w '%{http_code}' "${base}${path}")"
    [ "${got}" = "${expected}" ] || fail "GET ${path}: expected ${expected}, got ${got}"
}

assert_body() {
    path="$1"
    expected="$2"
    got="$(curl -s "${base}${path}")"
    [ "${got}" = "${expected}" ] || fail "GET ${path}: expected body '${expected}', got '${got}'"
}

wait_for_ready || fail "nginx did not become ready"

# --- 1. A missing content-hashed asset is a real 404, never the SPA shell ---
assert_status "/assets/missing.js" "404"

# --- 2. An existing asset and real app routes still serve correctly ---
assert_status "/assets/real.js" "200"
assert_body "/assets/real.js" "// real asset"
assert_status "/some/app/route" "200"
assert_body "/some/app/route" "SPA-SHELL"
assert_status "/definitely-not-a-real-path" "200"
assert_body "/definitely-not-a-real-path" "SPA-SHELL"

# --- 3. /healthz and /version.json serve their own content, not the shell ---
assert_status "/healthz" "200"
assert_body "/healthz" "ok"
assert_status "/version.json" "200"
assert_body "/version.json" '{"version":"test-1.2.3"}'

echo "OK: missing assets 404, app routes and existing assets serve correctly, healthz/version.json are not swallowed by the SPA fallback"
