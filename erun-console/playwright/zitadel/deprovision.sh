#!/usr/bin/env bash
# Remove exactly what provision.sh created — the login user, the OIDC app, and
# the project — through the Management API, then forget the generated env file.
#
# Tearing the containers down would erase these too, but doing it explicitly is
# what proves the run leaves the issuer as it found it, and keeps the harness
# usable against a Zitadel that outlives one run. Best effort by design: a run
# that failed halfway must still clean up whatever it did create.
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"
ENV_FILE="$SCRIPT_DIR/../.e2e-oidc.env"
[ -f "$ENV_FILE" ] || exit 0
# shellcheck disable=SC1090
. "$ENV_FILE"

VOL="erun-console-e2e-bootstrap"
SA="$(docker run --rm -v "$VOL":/b alpine cat /b/admin-sa.pat 2>/dev/null || true)"
if [ -z "$SA" ]; then
  rm -f "$ENV_FILE"
  exit 0
fi
auth=(-H "Authorization: Bearer $SA" -H "Content-Type: application/json")

drop() {
  local what="$1" path="$2"
  if curl -fsS "${auth[@]}" -X DELETE "$E2E_OIDC_ISSUER$path" >/dev/null 2>&1; then
    echo "==> removed $what"
  else
    echo "==> could not remove $what (already gone?)" >&2
  fi
}

[ -n "${E2E_OIDC_USER_ID:-}" ] && drop "user $E2E_OIDC_USER_ID" "/management/v1/users/$E2E_OIDC_USER_ID"
[ -n "${E2E_OIDC_APP_ID:-}" ] && drop "app $E2E_OIDC_APP_ID" "/management/v1/projects/$E2E_OIDC_PROJECT_ID/apps/$E2E_OIDC_APP_ID"
[ -n "${E2E_OIDC_PROJECT_ID:-}" ] && drop "project $E2E_OIDC_PROJECT_ID" "/management/v1/projects/$E2E_OIDC_PROJECT_ID"

rm -f "$ENV_FILE"
