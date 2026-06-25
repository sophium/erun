#!/usr/bin/env bash
# Canonical entry point for the console's OIDC sign-in e2e (issue #684).
#
# Brings up the FULL real stack — a local Zitadel v4 (core + Login V2 + proxy),
# a migrated erun-backend-api on its own Postgres, and the console dev server —
# provisions the OIDC SPA app headlessly, then runs the Playwright spec that
# drives the browser sign-in end to end. Everything is torn down on exit.
#
# Prerequisites on PATH: docker, go, atlas, yarn (+ a one-time
# `yarn install-browsers` for Chromium). Ports used: 8080 (Zitadel), 5173
# (console), 17055 (api), 5544 (api Postgres) — must be free.
#
# Anything passed through is forwarded to `playwright test`, so
# `./run.sh --headed` and `./run.sh --debug` work.
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"
REPO_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)"
CONSOLE_DIR="$SCRIPT_DIR/.."
API_DIR="$REPO_ROOT/erun-backend/erun-backend-api"
DB_DIR="$REPO_ROOT/erun-backend/erun-backend-db"
CACHE_DIR="$SCRIPT_DIR/.cache"
mkdir -p "$CACHE_DIR"

API_DB_CONTAINER="erun-console-e2e-apidb"
API_DB_URL="postgres://erun:erun@localhost:5544/erun?sslmode=disable"
EAPI_PID=""
VITE_PID=""

cleanup() {
  set +e
  [ -n "$VITE_PID" ] && kill "$VITE_PID" 2>/dev/null
  [ -n "$EAPI_PID" ] && kill "$EAPI_PID" 2>/dev/null
  docker rm -f "$API_DB_CONTAINER" >/dev/null 2>&1
  (cd "$SCRIPT_DIR/zitadel" && docker compose down -v >/dev/null 2>&1)
  rm -f "$SCRIPT_DIR/.e2e-oidc.env"
}
trap cleanup EXIT

echo "==> 1/5 Zitadel up + OIDC app provisioned"
bash "$SCRIPT_DIR/zitadel/provision.sh"
# shellcheck disable=SC1091
. "$SCRIPT_DIR/.e2e-oidc.env"

echo "==> 2/5 erun API Postgres + migrations"
docker rm -f "$API_DB_CONTAINER" >/dev/null 2>&1 || true
docker run -d --name "$API_DB_CONTAINER" -p 5544:5432 \
  -e POSTGRES_USER=erun -e POSTGRES_PASSWORD=erun -e POSTGRES_DB=erun postgres:18 >/dev/null
for _ in $(seq 1 30); do docker exec "$API_DB_CONTAINER" pg_isready -U erun >/dev/null 2>&1 && break; sleep 1; done
(cd "$DB_DIR" && atlas migrate apply --dir "file://migrations/default" --url "$API_DB_URL")

echo "==> 3/5 building + starting eapi (trusts $E2E_OIDC_ISSUER)"
(cd "$API_DIR" && go build -o "$CACHE_DIR/eapi" ./cmd/eapi)
ERUN_API_HOST=127.0.0.1 ERUN_API_PORT=17055 \
  ERUN_DATABASE_URL="$API_DB_URL" \
  ERUN_OIDC_ALLOWED_ISSUERS="$E2E_OIDC_ISSUER" \
  "$CACHE_DIR/eapi" >"$CACHE_DIR/eapi.log" 2>&1 &
EAPI_PID=$!
for _ in $(seq 1 30); do [ "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:17055/healthz 2>/dev/null)" = 204 ] && break; sleep 1; done

echo "==> 4/5 starting console dev server (OIDC -> Zitadel)"
(cd "$CONSOLE_DIR" && VITE_OIDC_ISSUER="$E2E_OIDC_ISSUER" VITE_OIDC_CLIENT_ID="$E2E_OIDC_CLIENT_ID" \
  VITE_API_PROXY_TARGET="http://127.0.0.1:17055" yarn dev --port 5173 --strictPort >"$CACHE_DIR/console.log" 2>&1) &
VITE_PID=$!
for _ in $(seq 1 30); do [ "$(curl -s -o /dev/null -w '%{http_code}' http://localhost:5173/ 2>/dev/null)" = 200 ] && break; sleep 1; done

echo "==> 5/5 running the Playwright OIDC sign-in spec"
export E2E_CONSOLE_URL="http://localhost:5173/"
(cd "$SCRIPT_DIR" && yarn playwright test "$@")
