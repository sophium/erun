#!/usr/bin/env bash
# Real end-to-end proof for erun#1107 Phase 3 / erun#763's console-facing half
# (erun#2024/erun#2026/erun#2035): a console session minted at erun:operate
# drives deploy/context_start/context_stop/resize over a REAL live MCP edge,
# and is refused every admin-only tool (exec_raw/delete/terraform/init) -- the
# blast-radius property those issues exist to hold.
#
# Brings up a real Postgres, a real erun-backend-api (its own MCP signer
# configured, no live IdP -- the desktop-signed dev-token flow
# erun-console/AGENTS.md documents), a real emcp instance (the same binary a
# deployed runtime pod runs) inside a throwaway rootful container -- only a
# container gets root to write the fixed /etc/erun/mcp-auth/desktopid.pub the
# edge's file:// verifier requires, this pod's own user does not -- and the
# console dev server itself. The console signs in as an ORDINARY tenant
# member (TenantUser, not the platform's own admin), because that is exactly
# who erun:operate exists for: an operator with no delete-environment
# entitlement, handed the narrower tier on purpose.
#
# Prerequisites on PATH: docker, go, atlas, yarn, openssl, python3.
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"
REPO_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)"
CONSOLE_DIR="$SCRIPT_DIR/.."
API_DIR="$REPO_ROOT/erun-backend/erun-backend-api"
DB_DIR="$REPO_ROOT/erun-backend/erun-backend-db"
MCP_DIR="$REPO_ROOT/erun-mcp"
CACHE_DIR="$SCRIPT_DIR/.cache-mcp-operate-scope"
rm -rf "$CACHE_DIR"
mkdir -p "$CACHE_DIR"

API_DB_CONTAINER="erun-console-e2e-mcp-apidb"
API_DB_PORT=5545
API_PORT=17057
CONSOLE_PORT=5175
MCP_PORT=28100
API_DB_URL="postgres://erun:erun@localhost:${API_DB_PORT}/erun?sslmode=disable"
ENV_NAME="e2e-operate-scope"
EMCP_IMAGE="erun-console-e2e-mcp-edge"
EMCP_CONTAINER="erun-console-e2e-mcp-edge"
EAPI_PGID=""
VITE_PGID=""

cleanup() {
  set +e
  [ -n "$VITE_PGID" ] && kill -- "-$VITE_PGID" 2>/dev/null
  [ -n "$EAPI_PGID" ] && kill -- "-$EAPI_PGID" 2>/dev/null
  docker rm -f "$EMCP_CONTAINER" >/dev/null 2>&1
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

# sign_token mints a desktop-signed EdDSA JWT by hand (openssl + python3, no
# extra Go module): header.claims base64url-encoded, signed raw (Ed25519 is a
# PureEdDSA scheme -- it signs the JWT signing input directly, no digest
# pre-hash) via `openssl pkeyutl`. Mirrors eruncommon.SignMCPToken exactly, so
# the resulting token verifies against the real, unmodified erun-backend-api.
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
require_free_port "$MCP_PORT" "mcp edge"

echo "==> 1/7 signing keys (API dev-bearer trust anchor + backend MCP signer)"
openssl genpkey -algorithm ED25519 -out "$CACHE_DIR/api-desktop.key" 2>/dev/null
openssl pkey -in "$CACHE_DIR/api-desktop.key" -pubout -out "$CACHE_DIR/api-desktop.pub" 2>/dev/null
openssl genpkey -algorithm ED25519 -out "$CACHE_DIR/mcp-signing.key" 2>/dev/null
openssl pkey -in "$CACHE_DIR/mcp-signing.key" -pubout -out "$CACHE_DIR/mcp-signing.pub" 2>/dev/null

echo "==> 2/7 erun API Postgres + migrations"
docker rm -f "$API_DB_CONTAINER" >/dev/null 2>&1 || true
docker run -d --name "$API_DB_CONTAINER" -p "${API_DB_PORT}:5432" \
  -e POSTGRES_USER=erun -e POSTGRES_PASSWORD=erun -e POSTGRES_DB=erun postgres:18 >/dev/null
for _ in $(seq 1 60); do
  docker exec "$API_DB_CONTAINER" pg_isready -U erun >/dev/null 2>&1 && break
  sleep 1
done
(cd "$DB_DIR" && atlas migrate apply --dir "file://migrations/default" --url "$API_DB_URL")

echo "==> 3/7 building + starting eapi (MCP signer configured, no live IdP)"
(cd "$API_DIR" && go build -o "$CACHE_DIR/eapi" ./cmd/eapi)
setsid env ERUN_API_HOST=127.0.0.1 ERUN_API_PORT="$API_PORT" \
  ERUN_DATABASE_URL="$API_DB_URL" \
  ERUN_API_DESKTOP_PUBLIC_KEY_PATH="$CACHE_DIR/api-desktop.pub" \
  ERUN_API_MCP_SIGNING_KEY_PATH="$CACHE_DIR/mcp-signing.key" \
  ERUN_TENANT="erun" \
  "$CACHE_DIR/eapi" >"$CACHE_DIR/eapi.log" 2>&1 &
EAPI_PGID=$!
await_http "http://127.0.0.1:${API_PORT}/healthz" 204 "erun api" "$CACHE_DIR/eapi.log"

echo "==> 4/7 bootstrapping identity: a platform admin registers the environment, an ordinary TenantUser drives it"
ADMIN_TOKEN="$(sign_token "$CACHE_DIR/api-desktop.key" "$CACHE_DIR/api-desktop.pub" "e2e-admin" "erun-api" 3600)"
# First authenticated call bootstraps the OPERATIONS tenant + this user as its
# admin (ReadAll+WriteAll) -- see erun-backend-api/AGENTS.md "Authentication".
curl -sf "http://127.0.0.1:${API_PORT}/v1/whoami" -H "Authorization: Bearer $ADMIN_TOKEN" >/dev/null
ENV_ID="$(curl -sf -X POST "http://127.0.0.1:${API_PORT}/v1/environments" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d "{\"name\":\"${ENV_NAME}\",\"type\":\"remote-agent\",\"adopt\":true,\"kubernetesContext\":\"none\"}" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["environmentId"])')"
ROLE_ID="$(curl -sf "http://127.0.0.1:${API_PORT}/v1/roles" -H "Authorization: Bearer $ADMIN_TOKEN" \
  | python3 -c 'import json,sys; roles=json.load(sys.stdin); print(next(r["roleId"] for r in roles if r["name"] == "TenantUser"))')"
curl -sf -X POST "http://127.0.0.1:${API_PORT}/v1/users" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d "{\"username\":\"e2e-operate-operator\",\"issuer\":\"file://${CACHE_DIR}/api-desktop.pub\",\"subject\":\"e2e-operate-operator\",\"roleIds\":[\"${ROLE_ID}\"]}" >/dev/null
OPERATOR_TOKEN="$(sign_token "$CACHE_DIR/api-desktop.key" "$CACHE_DIR/api-desktop.pub" "e2e-operate-operator" "erun-api" 3600)"

echo "==> 5/7 building the live MCP edge (real emcp, baked with the backend's own signing public key)"
(cd "$MCP_DIR" && go build -o "$CACHE_DIR/emcp" ./cmd/emcp)
cp "$CACHE_DIR/mcp-signing.pub" "$CACHE_DIR/desktopid.pub"
cat > "$CACHE_DIR/Dockerfile" <<'EOF'
FROM debian:12-slim
RUN mkdir -p /etc/erun/mcp-auth
COPY desktopid.pub /etc/erun/mcp-auth/desktopid.pub
COPY emcp /usr/local/bin/emcp
RUN chmod +x /usr/local/bin/emcp
ENTRYPOINT ["/usr/local/bin/emcp"]
EOF
docker build -q -t "$EMCP_IMAGE" "$CACHE_DIR" >/dev/null
docker rm -f "$EMCP_CONTAINER" >/dev/null 2>&1 || true
docker run -d --name "$EMCP_CONTAINER" -p "${MCP_PORT}:${MCP_PORT}" \
  -e ERUN_MCP_TRUSTED_ISSUER="file:///etc/erun/mcp-auth/desktopid.pub" \
  -e ERUN_MCP_AUDIENCE="erun-mcp:erun/${ENV_NAME}" \
  -e ERUN_TENANT="erun" \
  "$EMCP_IMAGE" \
  --host 0.0.0.0 --port "$MCP_PORT" --path /mcp --metrics-enabled=false \
  --tenant erun --environment "$ENV_NAME" --repo-path /tmp --kubernetes-context none --namespace "erun-${ENV_NAME}" >/dev/null
await_http "http://127.0.0.1:${MCP_PORT}/mcp" 401 "erun mcp edge"

echo "==> 6/7 console dev server, signed in as the ordinary operator"
setsid env VITE_DEV_BEARER_TOKEN="$OPERATOR_TOKEN" \
  VITE_API_PROXY_TARGET="http://127.0.0.1:${API_PORT}" \
  "$CONSOLE_DIR/node_modules/.bin/vite" "$CONSOLE_DIR" --port "$CONSOLE_PORT" --strictPort \
  >"$CACHE_DIR/console.log" 2>&1 &
VITE_PGID=$!
await_http "http://localhost:${CONSOLE_PORT}/" 200 "console dev server" "$CACHE_DIR/console.log"

echo "==> 7/7 running the spec"
export ERUN_E2E_CONSOLE_MCP_OPERATE=1
export E2E_CONSOLE_URL="http://localhost:${CONSOLE_PORT}/"
export E2E_MCP_ENV_NAME="$ENV_NAME"
export E2E_MCP_HOSTNAME="http://127.0.0.1:${MCP_PORT}"
(cd "$SCRIPT_DIR" && yarn playwright test tests/mcp-operate-scope.spec.ts "$@")
