#!/usr/bin/env bash
# Real end-to-end proof for erun#1692's browser-side attach edge
# (erun-console's src/mcp/attachClient.ts + AttachSessionForm): mints an
# erun:attach-scoped token and drives a REAL dtach/PTY session over a REAL
# WebSocket, from a real Chromium tab at a different origin than the edge --
# the one thing MCPAccessPanel.test.tsx's mocked WebSocket cannot prove, and
# the exact class of gap erun-console/playwright/tests/mcp-operate-scope.spec.ts
# found a real defect in for the sibling JSON-RPC edge (a cross-origin round
# trip nobody had actually run).
#
# Brings up a real Postgres, a real erun-backend-api (its own MCP signer
# configured, no live IdP -- the desktop-signed dev-token flow
# erun-console/AGENTS.md documents), a real emcp instance (the same binary a
# deployed runtime pod runs, with dtach installed so it can actually create a
# session) inside a throwaway rootful container -- only a container gets root
# to write the fixed /etc/erun/mcp-auth/desktopid.pub the edge's file://
# verifier requires -- and the console dev server itself.
#
# attachClient.ts's attachEdgeUrl() always builds a wss:// URL (necessary in
# production: every real deployed edge sits behind the platform's TLS
# ingress, and the console itself is served over https, so a plain ws://
# would be mixed-content-blocked). That means this harness cannot point a
# browser at the throwaway emcp container directly the way
# run-mcp-operate-scope.sh's plain-http JSON-RPC calls do -- a real TLS
# frontend is required to prove the browser side at all. A tiny Node TCP
# proxy terminates a self-signed cert in front of the container's plain-HTTP
# port; the spec's own browser context accepts that cert explicitly
# (ignoreHTTPSErrors), the same trust decision an operator's browser would
# make with a real CA-issued cert instead.
#
# Prerequisites on PATH: docker, go, atlas, yarn, openssl, python3, node.
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"
REPO_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)"
CONSOLE_DIR="$SCRIPT_DIR/.."
API_DIR="$REPO_ROOT/erun-backend/erun-backend-api"
DB_DIR="$REPO_ROOT/erun-backend/erun-backend-db"
MCP_DIR="$REPO_ROOT/erun-mcp"
CACHE_DIR="$SCRIPT_DIR/.cache-mcp-attach-session"
rm -rf "$CACHE_DIR"
mkdir -p "$CACHE_DIR"

API_DB_CONTAINER="erun-console-e2e-attach-apidb"
API_DB_PORT=5547
API_PORT=17059
CONSOLE_PORT=5177
MCP_PORT=28150
TLS_PORT=28151
API_DB_URL="postgres://erun:erun@localhost:${API_DB_PORT}/erun?sslmode=disable"
ENV_NAME="e2e-attach-session"
EMCP_IMAGE="erun-console-e2e-attach-edge"
EMCP_CONTAINER="erun-console-e2e-attach-edge"
EAPI_PGID=""
VITE_PGID=""
TLS_PID=""

cleanup() {
  set +e
  [ -n "$VITE_PGID" ] && kill -- "-$VITE_PGID" 2>/dev/null
  [ -n "$EAPI_PGID" ] && kill -- "-$EAPI_PGID" 2>/dev/null
  [ -n "$TLS_PID" ] && kill "$TLS_PID" 2>/dev/null
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
# PureEdDSA scheme) via `openssl pkeyutl`. Mirrors eruncommon.SignMCPToken
# exactly, so the resulting token verifies against the real, unmodified
# erun-backend-api.
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
require_free_port "$TLS_PORT" "mcp edge tls front"

echo "==> 1/8 signing keys (API dev-bearer trust anchor + backend MCP signer)"
openssl genpkey -algorithm ED25519 -out "$CACHE_DIR/api-desktop.key" 2>/dev/null
openssl pkey -in "$CACHE_DIR/api-desktop.key" -pubout -out "$CACHE_DIR/api-desktop.pub" 2>/dev/null
openssl genpkey -algorithm ED25519 -out "$CACHE_DIR/mcp-signing.key" 2>/dev/null
openssl pkey -in "$CACHE_DIR/mcp-signing.key" -pubout -out "$CACHE_DIR/mcp-signing.pub" 2>/dev/null

echo "==> 2/8 erun API Postgres + migrations"
docker rm -f "$API_DB_CONTAINER" >/dev/null 2>&1 || true
docker run -d --name "$API_DB_CONTAINER" -p "${API_DB_PORT}:5432" \
  -e POSTGRES_USER=erun -e POSTGRES_PASSWORD=erun -e POSTGRES_DB=erun postgres:18 >/dev/null
for _ in $(seq 1 60); do
  docker exec "$API_DB_CONTAINER" pg_isready -U erun >/dev/null 2>&1 && break
  sleep 1
done
(cd "$DB_DIR" && atlas migrate apply --dir "file://migrations/default" --url "$API_DB_URL")

echo "==> 3/8 building + starting eapi (MCP signer configured, no live IdP)"
(cd "$API_DIR" && go build -o "$CACHE_DIR/eapi" ./cmd/eapi)
setsid env ERUN_API_HOST=127.0.0.1 ERUN_API_PORT="$API_PORT" \
  ERUN_DATABASE_URL="$API_DB_URL" \
  ERUN_API_DESKTOP_PUBLIC_KEY_PATH="$CACHE_DIR/api-desktop.pub" \
  ERUN_API_MCP_SIGNING_KEY_PATH="$CACHE_DIR/mcp-signing.key" \
  ERUN_TENANT="erun" \
  "$CACHE_DIR/eapi" >"$CACHE_DIR/eapi.log" 2>&1 &
EAPI_PGID=$!
await_http "http://127.0.0.1:${API_PORT}/healthz" 204 "erun api" "$CACHE_DIR/eapi.log"

echo "==> 4/8 bootstrapping identity: a platform admin registers the environment, an ordinary TenantUser drives it"
ADMIN_TOKEN="$(sign_token "$CACHE_DIR/api-desktop.key" "$CACHE_DIR/api-desktop.pub" "e2e-admin" "erun-api" 3600)"
curl -sf "http://127.0.0.1:${API_PORT}/v1/whoami" -H "Authorization: Bearer $ADMIN_TOKEN" >/dev/null
ENV_ID="$(curl -sf -X POST "http://127.0.0.1:${API_PORT}/v1/environments" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d "{\"name\":\"${ENV_NAME}\",\"type\":\"remote-agent\",\"adopt\":true,\"kubernetesContext\":\"none\"}" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["environmentId"])')"
ROLE_ID="$(curl -sf "http://127.0.0.1:${API_PORT}/v1/roles" -H "Authorization: Bearer $ADMIN_TOKEN" \
  | python3 -c 'import json,sys; roles=json.load(sys.stdin); print(next(r["roleId"] for r in roles if r["name"] == "TenantUser"))')"
curl -sf -X POST "http://127.0.0.1:${API_PORT}/v1/users" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H "Content-Type: application/json" \
  -d "{\"username\":\"e2e-attach-operator\",\"issuer\":\"file://${CACHE_DIR}/api-desktop.pub\",\"subject\":\"e2e-attach-operator\",\"roleIds\":[\"${ROLE_ID}\"]}" >/dev/null
OPERATOR_TOKEN="$(sign_token "$CACHE_DIR/api-desktop.key" "$CACHE_DIR/api-desktop.pub" "e2e-attach-operator" "erun-api" 3600)"

echo "==> 5/8 building the live MCP edge (real emcp, with dtach installed so it can actually create a session)"
(cd "$MCP_DIR" && go build -o "$CACHE_DIR/emcp" ./cmd/emcp)
cp "$CACHE_DIR/mcp-signing.pub" "$CACHE_DIR/desktopid.pub"
cat > "$CACHE_DIR/Dockerfile" <<'EOF'
FROM debian:12-slim
RUN apt-get update && apt-get install -y --no-install-recommends dtach && rm -rf /var/lib/apt/lists/*
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

echo "==> 6/8 self-signed TLS front for the attach edge (attachClient.ts always dials wss://, matching the real ingress every deployed edge sits behind)"
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -keyout "$CACHE_DIR/tls-key.pem" \
  -out "$CACHE_DIR/tls-cert.pem" -days 1 -nodes -subj "/CN=127.0.0.1" >/dev/null 2>&1
cat > "$CACHE_DIR/tls-terminate.mjs" <<'EOF'
import tls from 'node:tls';
import net from 'node:net';
import fs from 'node:fs';

const [, , listenPort, certPath, keyPath, targetHost, targetPort] = process.argv;

const server = tls.createServer(
  { cert: fs.readFileSync(certPath), key: fs.readFileSync(keyPath) },
  (socket) => {
    const upstream = net.connect(Number(targetPort), targetHost, () => {
      socket.pipe(upstream);
      upstream.pipe(socket);
    });
    upstream.on('error', () => socket.destroy());
    socket.on('error', () => upstream.destroy());
  },
);

server.listen(Number(listenPort), '127.0.0.1', () => {
  process.stdout.write(`tls-terminate: ${listenPort} -> ${targetHost}:${targetPort}\n`);
});
EOF
node "$CACHE_DIR/tls-terminate.mjs" "$TLS_PORT" "$CACHE_DIR/tls-cert.pem" "$CACHE_DIR/tls-key.pem" 127.0.0.1 "$MCP_PORT" \
  >"$CACHE_DIR/tls-terminate.log" 2>&1 &
TLS_PID=$!
for _ in $(seq 1 30); do
  (exec 3<>"/dev/tcp/127.0.0.1/$TLS_PORT") 2>/dev/null && exec 3>&- && break
  sleep 1
done

echo "==> 7/8 console dev server, signed in as the ordinary operator"
setsid env VITE_DEV_BEARER_TOKEN="$OPERATOR_TOKEN" \
  VITE_API_PROXY_TARGET="http://127.0.0.1:${API_PORT}" \
  "$CONSOLE_DIR/node_modules/.bin/vite" "$CONSOLE_DIR" --port "$CONSOLE_PORT" --strictPort \
  >"$CACHE_DIR/console.log" 2>&1 &
VITE_PGID=$!
await_http "http://localhost:${CONSOLE_PORT}/" 200 "console dev server" "$CACHE_DIR/console.log"

echo "==> 8/8 running the spec"
export ERUN_E2E_CONSOLE_MCP_ATTACH=1
export E2E_CONSOLE_URL="http://localhost:${CONSOLE_PORT}/"
export E2E_ATTACH_ENV_NAME="$ENV_NAME"
export E2E_ATTACH_HOSTNAME="127.0.0.1:${TLS_PORT}"
(cd "$SCRIPT_DIR" && yarn playwright test tests/mcp-attach-session.spec.ts "$@")
