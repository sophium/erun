#!/usr/bin/env bash
# Real end-to-end proof for the console's plain-REST write surfaces that,
# before this, were only ever exercised against a mocked `fetch`:
# ProvisionPanel (src/provision/) and TenantsPanel (src/tenants/). Both go
# through the exact same same-origin httpBaseQuery transport `GET /v1/config`
# already proves live (erun-console/AGENTS.md's "Running against a real
# erun-backend-api (dev)") -- unlike the MCP JSON-RPC edge and the WebSocket
# attach edge, neither of which is a separate host with its own wire
# protocol, so neither carries the cross-origin/browser-security-policy risk
# class that actually broke those two. This suite exists to prove that
# structural argument rather than merely assert it: a real Postgres row, a
# real JSON response, parsed by the console's real RTK Query endpoint
# definitions -- the one thing a mocked fetch cannot catch is a schema drift
# between what the console's TypeScript types expect and what the real API
# actually returns.
#
# erun-backend-api's identity administration (UsersPanel/OrgSettingsPanel/
# InvitesPanel/SmtpSettingsPanel) needs a real Zitadel Management API
# (unset ERUN_ZITADEL_MANAGEMENT_API_URL disables /v1/identity/* entirely) and
# EnvironmentsPanel's deploy mutation needs a real job/cluster runner to
# complete -- both a materially heavier lift than this suite's scope. See
# erun-console/AGENTS.md for the disclosed gap that leaves open.
#
# Prerequisites on PATH: docker, go, atlas, yarn, openssl, python3.
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"
REPO_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)"
CONSOLE_DIR="$SCRIPT_DIR/.."
API_DIR="$REPO_ROOT/erun-backend/erun-backend-api"
DB_DIR="$REPO_ROOT/erun-backend/erun-backend-db"
CACHE_DIR="$SCRIPT_DIR/.cache-rest-surfaces"
rm -rf "$CACHE_DIR"
mkdir -p "$CACHE_DIR"

API_DB_CONTAINER="erun-console-e2e-rest-apidb"
API_DB_PORT=5548
API_PORT=17060
CONSOLE_PORT=5178
API_DB_URL="postgres://erun:erun@localhost:${API_DB_PORT}/erun?sslmode=disable"
EAPI_PGID=""
VITE_PGID=""

cleanup() {
  set +e
  [ -n "$VITE_PGID" ] && kill -- "-$VITE_PGID" 2>/dev/null
  [ -n "$EAPI_PGID" ] && kill -- "-$EAPI_PGID" 2>/dev/null
  docker rm -f "$API_DB_CONTAINER" >/dev/null 2>&1
}
trap cleanup EXIT

require_free_port() {
  local port="$1" what="$2"
  if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
    exec 3>&-
    echo "port $port ($what) is already in use; free it before running the e2e" >&2
    exit 1
  fi
}

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

sign_token() {
  local key_path="$1" issuer_path="$2" subject="$3" audience="$4" ttl_seconds="$5"
  python3 - "$issuer_path" "$subject" "$audience" "$ttl_seconds" <<'PYEOF' > "$CACHE_DIR/signing_input.txt"
import base64, json, sys, time
issuer_path, subject, audience, ttl = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
def b64u(obj):
    data = json.dumps(obj, separators=(',', ':')).encode()
    return base64.urlsafe_b64encode(data).rstrip(b'=').decode()
now = int(time.time())
header = {"alg": "EdDSA", "typ": "JWT"}
claims = {"iss": f"file://{issuer_path}", "sub": subject, "aud": audience, "iat": now, "exp": now + ttl}
print(b64u(header) + "." + b64u(claims), end="")
PYEOF
  printf '%s' "$(cat "$CACHE_DIR/signing_input.txt")" > "$CACHE_DIR/signing_input.bin"
  openssl pkeyutl -sign -inkey "$key_path" -rawin -in "$CACHE_DIR/signing_input.bin" -out "$CACHE_DIR/sig.bin" 2>/dev/null
  local sig
  sig="$(python3 -c "
import base64
print(base64.urlsafe_b64encode(open('$CACHE_DIR/sig.bin','rb').read()).rstrip(b'=').decode())
")"
  printf '%s.%s' "$(cat "$CACHE_DIR/signing_input.txt")" "$sig"
}

require_free_port "$API_DB_PORT" "api postgres"
require_free_port "$API_PORT" "api"
require_free_port "$CONSOLE_PORT" "console"

echo "==> 1/5 signing key (API dev-bearer trust anchor)"
openssl genpkey -algorithm ED25519 -out "$CACHE_DIR/api-desktop.key" 2>/dev/null
openssl pkey -in "$CACHE_DIR/api-desktop.key" -pubout -out "$CACHE_DIR/api-desktop.pub" 2>/dev/null

echo "==> 2/5 erun API Postgres + migrations"
docker rm -f "$API_DB_CONTAINER" >/dev/null 2>&1 || true
docker run -d --name "$API_DB_CONTAINER" -p "${API_DB_PORT}:5432" \
  -e POSTGRES_USER=erun -e POSTGRES_PASSWORD=erun -e POSTGRES_DB=erun postgres:18 >/dev/null
for _ in $(seq 1 60); do
  docker exec "$API_DB_CONTAINER" pg_isready -U erun >/dev/null 2>&1 && break
  sleep 1
done
(cd "$DB_DIR" && atlas migrate apply --dir "file://migrations/default" --url "$API_DB_URL")

echo "==> 3/5 building + starting eapi (no cloud provisioner -- registration-only aliases/contexts; no Zitadel)"
(cd "$API_DIR" && go build -o "$CACHE_DIR/eapi" ./cmd/eapi)
# ERUN_SECRETS_KEY gates the whole cloud-provider-alias route (server.go only
# calls routes.RegisterCloudProviderAliasRoutes when options.Cipher != nil) --
# unset, PUT /v1/cloud-provider-aliases/{alias} 404s outright rather than the
# handled 501 a nil-signer/nil-provisioner path uses elsewhere in this API.
setsid env ERUN_API_HOST=127.0.0.1 ERUN_API_PORT="$API_PORT" \
  ERUN_DATABASE_URL="$API_DB_URL" \
  ERUN_API_DESKTOP_PUBLIC_KEY_PATH="$CACHE_DIR/api-desktop.pub" \
  ERUN_SECRETS_KEY="$(openssl rand -base64 32)" \
  ERUN_TENANT="erun" \
  "$CACHE_DIR/eapi" >"$CACHE_DIR/eapi.log" 2>&1 &
EAPI_PGID=$!
await_http "http://127.0.0.1:${API_PORT}/healthz" 204 "erun api" "$CACHE_DIR/eapi.log"

echo "==> 4/5 bootstrapping identity: the first authenticated call becomes this tenant's OPERATIONS admin"
ADMIN_TOKEN="$(sign_token "$CACHE_DIR/api-desktop.key" "$CACHE_DIR/api-desktop.pub" "e2e-rest-admin" "erun-api" 3600)"
curl -sf "http://127.0.0.1:${API_PORT}/v1/whoami" -H "Authorization: Bearer $ADMIN_TOKEN" >/dev/null

echo "==> 5/5 console dev server, signed in as the OPERATIONS admin"
setsid env VITE_DEV_BEARER_TOKEN="$ADMIN_TOKEN" \
  VITE_API_PROXY_TARGET="http://127.0.0.1:${API_PORT}" \
  "$CONSOLE_DIR/node_modules/.bin/vite" "$CONSOLE_DIR" --port "$CONSOLE_PORT" --strictPort \
  >"$CACHE_DIR/console.log" 2>&1 &
VITE_PGID=$!
await_http "http://localhost:${CONSOLE_PORT}/" 200 "console dev server" "$CACHE_DIR/console.log"

export ERUN_E2E_CONSOLE_REST=1
export E2E_CONSOLE_URL="http://localhost:${CONSOLE_PORT}/"
(cd "$SCRIPT_DIR" && yarn playwright test tests/rest-surfaces.spec.ts "$@")
