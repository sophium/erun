#!/usr/bin/env bash
# Real-browser proof that the console's unauthenticated landing page never
# scrolls horizontally at phone widths.
#
# The boundary this suite proves is CSS layout in a real engine. The app's
# Vitest suite runs in jsdom, which implements no layout at all --
# `documentElement.scrollWidth` is always 0 there, so it cannot observe this
# bug even in principle, and a geometry sweep in that suite would be a test of
# nothing. Nothing else needs provisioning: the landing screen renders from
# bundled defaults with no token, and platform discovery failing (there is
# deliberately no API behind this suite) is exactly the "fall back, don't
# hard-fail" path `fetchPlatformConfig` already defines. So this runner builds
# the console, serves the static bundle, and measures it.
#
# Prerequisites on PATH: yarn, python3, curl.
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"
CONSOLE_DIR="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)"
CACHE_DIR="$SCRIPT_DIR/.cache-landing-layout"
rm -rf "$CACHE_DIR"
mkdir -p "$CACHE_DIR"

# Disjoint from every other runner's port set (5173/5175/5177/5178).
CONSOLE_PORT=5179
SERVE_PGID=""

cleanup() {
  set +e
  [ -n "$SERVE_PGID" ] && kill -- "-$SERVE_PGID" 2>/dev/null
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

for tool in yarn python3 curl; do
  command -v "$tool" >/dev/null 2>&1 || {
    echo "$tool is required on PATH" >&2
    exit 1
  }
done

require_free_port "$CONSOLE_PORT" "console static bundle"

echo "==> 1/2 building the console bundle (the landing page is measured as built, not from the dev server)"
(cd "$CONSOLE_DIR" && yarn build)

echo "==> 2/2 serving dist/ and measuring the front door in Chromium"
setsid python3 -m http.server "$CONSOLE_PORT" --bind 127.0.0.1 \
  --directory "$CONSOLE_DIR/dist" >"$CACHE_DIR/serve.log" 2>&1 &
SERVE_PGID=$!
await_http "http://127.0.0.1:${CONSOLE_PORT}/" 200 "console static bundle" "$CACHE_DIR/serve.log"

export ERUN_E2E_CONSOLE_LANDING_LAYOUT=1
export E2E_CONSOLE_URL="http://127.0.0.1:${CONSOLE_PORT}/"
(cd "$SCRIPT_DIR" && yarn playwright test tests/landing-no-horizontal-scroll.spec.ts "$@")
