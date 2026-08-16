#!/usr/bin/env bash
# Canonical entry point for the console's OIDC sign-in e2e.
#
# Brings up the FULL real stack — a local Zitadel v4 (core + Login V2 + proxy),
# a migrated erun-backend-api on its own Postgres, and the console dev server —
# provisions this run's own OIDC project, SPA app and login user, then runs the
# Playwright spec that drives the browser sign-in end to end. Everything it
# created (Zitadel objects first, then the containers and processes) is removed
# on exit, pass or fail.
#
# Prerequisites on PATH: docker, go, atlas, yarn, python3 (+ a one-time
# `yarn install-browsers` for Chromium). Ports used: 8080 (Zitadel), 5173
# (console), 17055 (api), 5544 (api Postgres) — must be free.
#
# Anything passed through is forwarded to `playwright test`, so `./run.sh
# --headed` and `./run.sh --debug` work.
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
EAPI_PGID=""
VITE_PGID=""

cleanup() {
  set +e
  # Zitadel objects first: they are removed through the API, which needs the
  # instance still running.
  bash "$SCRIPT_DIR/zitadel/deprovision.sh"
  # Whole process groups: `yarn dev` and the go binary spawn children that
  # outlive a kill aimed at the parent, and a surviving dev server would serve
  # the next run the previous run's client id.
  [ -n "$VITE_PGID" ] && kill -- "-$VITE_PGID" 2>/dev/null
  [ -n "$EAPI_PGID" ] && kill -- "-$EAPI_PGID" 2>/dev/null
  docker rm -f "$API_DB_CONTAINER" >/dev/null 2>&1
  bash "$SCRIPT_DIR/zitadel/stack.sh" down
}
trap cleanup EXIT

# A port already answering means something else — most likely a leaked server
# from an earlier run — would be mistaken for this run's stack. Refuse rather
# than test against it.
require_free_port() {
  local port="$1" what="$2"
  if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
    exec 3>&-
    echo "port $port ($what) is already in use; free it before running the e2e" >&2
    exit 1
  fi
}

# await_http blocks on the observable condition rather than a sleep, and fails
# the run when the service never answers instead of letting the spec discover it.
await_http() {
  local url="$1" want="$2" what="$3" log="${4:-}" code=""
  for _ in $(seq 1 60); do
    code="$(curl -s -o /dev/null -w '%{http_code}' "$url" 2>/dev/null || echo 000)"
    [ "$code" = "$want" ] && return 0
    sleep 1
  done
  echo "==> $what never answered $want at $url (last=$code)" >&2
  [ -n "$log" ] && [ -f "$log" ] && tail -30 "$log" >&2
  return 1
}

require_free_port 8080 zitadel
require_free_port 5173 console
require_free_port 17055 api
require_free_port 5544 "api postgres"

echo "==> 1/5 Zitadel v4 up (core + Login V2 + proxy)"
bash "$SCRIPT_DIR/zitadel/stack.sh" up

echo "==> 2/5 provisioning this run's OIDC project, app and login user"
bash "$SCRIPT_DIR/zitadel/provision.sh"
# shellcheck disable=SC1091
. "$SCRIPT_DIR/.e2e-oidc.env"

echo "==> 3/5 erun API Postgres + migrations"
docker rm -f "$API_DB_CONTAINER" >/dev/null 2>&1 || true
docker run -d --name "$API_DB_CONTAINER" -p 5544:5432 \
  -e POSTGRES_USER=erun -e POSTGRES_PASSWORD=erun -e POSTGRES_DB=erun postgres:18 >/dev/null
for _ in $(seq 1 60); do
  docker exec "$API_DB_CONTAINER" pg_isready -U erun >/dev/null 2>&1 && break
  sleep 1
done
(cd "$DB_DIR" && atlas migrate apply --dir "file://migrations/default" --url "$API_DB_URL")

echo "==> 4/5 building + starting eapi (trusts $E2E_OIDC_ISSUER)"
(cd "$API_DIR" && go build -o "$CACHE_DIR/eapi" ./cmd/eapi)
setsid env ERUN_API_HOST=127.0.0.1 ERUN_API_PORT=17055 \
  ERUN_DATABASE_URL="$API_DB_URL" \
  ERUN_OIDC_ALLOWED_ISSUERS="$E2E_OIDC_ISSUER" \
  "$CACHE_DIR/eapi" >"$CACHE_DIR/eapi.log" 2>&1 &
EAPI_PGID=$!
await_http http://127.0.0.1:17055/healthz 204 "erun api" "$CACHE_DIR/eapi.log"

echo "==> 5/5 console dev server + the sign-in spec"
setsid env VITE_OIDC_ISSUER="$E2E_OIDC_ISSUER" VITE_OIDC_CLIENT_ID="$E2E_OIDC_CLIENT_ID" \
  VITE_API_PROXY_TARGET="http://127.0.0.1:17055" \
  "$CONSOLE_DIR/node_modules/.bin/vite" "$CONSOLE_DIR" --port 5173 --strictPort \
  >"$CACHE_DIR/console.log" 2>&1 &
VITE_PGID=$!
await_http http://localhost:5173/ 200 "console dev server" "$CACHE_DIR/console.log"

# The gate the spec checks: only a run that actually stood this stack up may
# execute it. Without it the spec skips rather than failing against no IdP.
export ERUN_E2E_CONSOLE_OIDC=1
export E2E_CONSOLE_URL="http://localhost:5173/"
(cd "$SCRIPT_DIR" && yarn playwright test "$@")
