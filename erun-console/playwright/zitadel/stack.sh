#!/usr/bin/env bash
# Container lifecycle for the e2e's real Zitadel v4 — `stack.sh up` / `stack.sh down`.
#
# v4 is not a single container: core serves no interactive login at all (a lone
# core returns {"code":5,"message":"Not Found"} at /ui/v2/login), so the OIDC
# authorize endpoint has no page to render. The faithful topology is core + the
# separate zitadel-login ("Login V2") container + a reverse proxy unifying them
# under one origin, which is what the console's redirect_uri and the issuer must
# agree on. `start-from-init` writes two machine-user PATs to a shared volume:
# an org-owner service account (provision.sh drives the Management API with it)
# and the IAM_LOGIN_CLIENT PAT the login container authenticates with.
#
# Plain `docker run` rather than compose: `docker` is the only container tool
# this repository's harnesses assume. Readiness is polled over HTTP through the
# proxy rather than via container healthchecks, because `docker run --health-cmd`
# always runs through /bin/sh and the Zitadel images are distroless. The proxy
# therefore starts first; its nginx `resolver` makes upstreams resolve per
# request, so it tolerates upstreams that do not exist yet.
set -euo pipefail

ZITADEL_VERSION="v4.15.3"
POSTGRES_IMAGE="postgres:18"
PROXY_IMAGE="nginx:1.27-alpine"

NET="erun-console-e2e-znet"
VOL="erun-console-e2e-bootstrap"
PG="erun-console-e2e-zdb"
API="erun-console-e2e-zitadel"
LOGIN="erun-console-e2e-zlogin"
PROXY="erun-console-e2e-zproxy"

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"
ZITADEL_PORT="${E2E_ZITADEL_PORT:-8080}"
ISSUER="http://localhost:$ZITADEL_PORT"

down() {
  for c in "$PROXY" "$LOGIN" "$API" "$PG"; do
    docker rm -f "$c" >/dev/null 2>&1 || true
  done
  docker volume rm -f "$VOL" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
}

# await_http blocks until the URL answers with the expected status, then fails
# loudly with the named container's log rather than leaving a silent timeout.
await_http() {
  local url="$1" want="$2" container="$3" tries="${4:-120}" code=""
  for _ in $(seq 1 "$tries"); do
    code="$(curl -fsS -o /dev/null -w '%{http_code}' "$url" 2>/dev/null || echo 000)"
    [ "$code" = "$want" ] && return 0
    sleep 2
  done
  echo "==> $url never returned $want (last=$code); last log lines of $container:" >&2
  docker logs --tail 40 "$container" >&2 2>&1 || true
  return 1
}

up() {
  down
  docker network create "$NET" >/dev/null
  docker volume create "$VOL" >/dev/null

  docker run -d --name "$PG" --network "$NET" \
    -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=zitadel \
    "$POSTGRES_IMAGE" >/dev/null
  for _ in $(seq 1 60); do
    docker exec "$PG" pg_isready -U postgres >/dev/null 2>&1 && break
    sleep 2
  done

  docker run -d --name "$PROXY" --network "$NET" -p "$ZITADEL_PORT:8080" \
    -v "$SCRIPT_DIR/nginx.conf":/etc/nginx/nginx.conf:ro \
    "$PROXY_IMAGE" >/dev/null

  # ZITADEL_EXTERNAL* is the issuer the console, the browser and the API all
  # name: it must be the proxy's address, not the container's, or discovery
  # hands the browser endpoints it cannot reach.
  docker run -d --name "$API" --network "$NET" --user 0 \
    -v "$VOL":/zitadel/bootstrap:rw \
    -e ZITADEL_PORT=8080 \
    -e ZITADEL_EXTERNALDOMAIN=localhost \
    -e ZITADEL_EXTERNALPORT="$ZITADEL_PORT" \
    -e ZITADEL_EXTERNALSECURE=false \
    -e ZITADEL_TLS_ENABLED=false \
    -e ZITADEL_DATABASE_POSTGRES_HOST="$PG" \
    -e ZITADEL_DATABASE_POSTGRES_PORT=5432 \
    -e ZITADEL_DATABASE_POSTGRES_DATABASE=zitadel \
    -e ZITADEL_DATABASE_POSTGRES_USER_USERNAME=postgres \
    -e ZITADEL_DATABASE_POSTGRES_USER_PASSWORD=postgres \
    -e ZITADEL_DATABASE_POSTGRES_USER_SSL_MODE=disable \
    -e ZITADEL_DATABASE_POSTGRES_ADMIN_USERNAME=postgres \
    -e ZITADEL_DATABASE_POSTGRES_ADMIN_PASSWORD=postgres \
    -e ZITADEL_DATABASE_POSTGRES_ADMIN_SSL_MODE=disable \
    -e ZITADEL_FIRSTINSTANCE_ORG_NAME=erun \
    -e ZITADEL_FIRSTINSTANCE_ORG_HUMAN_USERNAME=zadmin \
    -e ZITADEL_FIRSTINSTANCE_ORG_HUMAN_PASSWORD='Password1!' \
    -e ZITADEL_FIRSTINSTANCE_ORG_HUMAN_PASSWORDCHANGEREQUIRED=false \
    -e ZITADEL_FIRSTINSTANCE_ORG_HUMAN_EMAIL_ADDRESS=zadmin@erun.local \
    -e ZITADEL_FIRSTINSTANCE_ORG_HUMAN_EMAIL_VERIFIED=true \
    -e ZITADEL_FIRSTINSTANCE_PATPATH=/zitadel/bootstrap/admin-sa.pat \
    -e ZITADEL_FIRSTINSTANCE_ORG_MACHINE_MACHINE_USERNAME=admin-sa \
    -e ZITADEL_FIRSTINSTANCE_ORG_MACHINE_MACHINE_NAME='Admin Service Account' \
    -e ZITADEL_FIRSTINSTANCE_ORG_MACHINE_PAT_EXPIRATIONDATE='2030-01-01T00:00:00Z' \
    -e ZITADEL_FIRSTINSTANCE_LOGINCLIENTPATPATH=/zitadel/bootstrap/login-client.pat \
    -e ZITADEL_FIRSTINSTANCE_ORG_LOGINCLIENT_MACHINE_USERNAME=login-client \
    -e ZITADEL_FIRSTINSTANCE_ORG_LOGINCLIENT_MACHINE_NAME='Login Client' \
    -e ZITADEL_FIRSTINSTANCE_ORG_LOGINCLIENT_PAT_EXPIRATIONDATE='2030-01-01T00:00:00Z' \
    -e ZITADEL_DEFAULTINSTANCE_FEATURES_LOGINV2_REQUIRED=true \
    -e ZITADEL_DEFAULTINSTANCE_FEATURES_LOGINV2_BASEURI="$ISSUER/ui/v2/login/" \
    -e ZITADEL_OIDC_DEFAULTLOGINURLV2="$ISSUER/ui/v2/login/login?authRequest=" \
    -e ZITADEL_OIDC_DEFAULTLOGOUTURLV2="$ISSUER/ui/v2/login/logout?post_logout_redirect=" \
    "ghcr.io/zitadel/zitadel:$ZITADEL_VERSION" \
    start-from-init --masterkey 'MasterkeyNeedsToHave32Characters' --tlsMode disabled >/dev/null
  await_http "$ISSUER/debug/healthz" 200 "$API" 180

  # The login container reads the IAM_LOGIN_CLIENT PAT `start-from-init` wrote,
  # so it can only start once core has finished initialising.
  docker run -d --name "$LOGIN" --network "$NET" --user 0 \
    -v "$VOL":/zitadel/bootstrap:ro \
    -e ZITADEL_API_URL="http://$API:8080" \
    -e NEXT_PUBLIC_BASE_PATH=/ui/v2/login \
    -e ZITADEL_SERVICE_USER_TOKEN_FILE=/zitadel/bootstrap/login-client.pat \
    -e CUSTOM_REQUEST_HEADERS="Host:localhost:$ZITADEL_PORT,X-Forwarded-Proto:http" \
    "ghcr.io/zitadel/zitadel-login:$ZITADEL_VERSION" >/dev/null
  await_http "$ISSUER/ui/v2/login/healthy" 200 "$LOGIN" 120
}

case "${1:-}" in
  up) up ;;
  down) down ;;
  *)
    echo "usage: $0 up|down" >&2
    exit 2
    ;;
esac
