#!/bin/sh
# Publish the pre-built Docusaurus static site (baked into the image at $SITE_DIR)
# to a Cloudflare Pages project via wrangler.
#
# Required environment variables (provided by the helm chart from a Secret + values):
#   CLOUDFLARE_API_TOKEN   — Cloudflare API token with `Pages:Edit` scope
#   CLOUDFLARE_ACCOUNT_ID  — Cloudflare account id that owns the Pages project
#   CF_PAGES_PROJECT       — Cloudflare Pages project name (e.g. erun-docs)
#   CF_PAGES_BRANCH        — Branch alias the deploy is published under (e.g. main, preview)
#
# wrangler reads CLOUDFLARE_API_TOKEN and CLOUDFLARE_ACCOUNT_ID directly.
set -eu

: "${SITE_DIR:?SITE_DIR is required}"
: "${CLOUDFLARE_API_TOKEN:?CLOUDFLARE_API_TOKEN is required}"
: "${CLOUDFLARE_ACCOUNT_ID:?CLOUDFLARE_ACCOUNT_ID is required}"
: "${CF_PAGES_PROJECT:?CF_PAGES_PROJECT is required}"
: "${CF_PAGES_BRANCH:?CF_PAGES_BRANCH is required}"

if [ ! -d "$SITE_DIR" ]; then
  echo "error: site directory $SITE_DIR does not exist" >&2
  exit 1
fi

# Ensure the Direct-Upload Pages project exists — `wrangler pages deploy` errors
# if it is missing. Create it on first run; a re-run tolerates "already exists"
# but still surfaces real failures (e.g. a token without Pages:Edit).
echo "ensuring Cloudflare Pages project '$CF_PAGES_PROJECT' exists"
if ! create_out="$(wrangler pages project create "$CF_PAGES_PROJECT" --production-branch="$CF_PAGES_BRANCH" 2>&1)"; then
  if printf '%s' "$create_out" | grep -qi 'already exists'; then
    echo "Cloudflare Pages project '$CF_PAGES_PROJECT' already exists"
  else
    printf '%s\n' "$create_out" >&2
    echo "error: failed to create Cloudflare Pages project '$CF_PAGES_PROJECT'" >&2
    exit 1
  fi
fi

echo "publishing $SITE_DIR to Cloudflare Pages project=$CF_PAGES_PROJECT branch=$CF_PAGES_BRANCH"
exec wrangler pages deploy "$SITE_DIR" \
  --project-name="$CF_PAGES_PROJECT" \
  --branch="$CF_PAGES_BRANCH" \
  --commit-dirty=true
