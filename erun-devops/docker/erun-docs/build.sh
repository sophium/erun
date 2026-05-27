#!/usr/bin/env bash
# Wrapper invoked by `erun build` in non-local environments. Mirrors the
# convention used by other erun-devops Docker images: build the multi-arch
# image with the tag erun computed for this environment.
#
# This script intentionally stays tiny — it just shells out to docker. erun
# itself owns multi-arch orchestration, fingerprint cache promotion, and
# manifest assembly when invoked as `erun build` / `erun build --release`.
#
# When running this script directly (without erun), set IMAGE_TAG to the
# desired final tag, e.g.
#   IMAGE_TAG=ghcr.io/sophium/erun-docs:1.0.76 ./build.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

IMAGE_TAG="${IMAGE_TAG:-erun-docs:dev}"
PLATFORM="${PLATFORM:-linux/amd64}"

cd "$PROJECT_ROOT"
docker build \
  --platform "$PLATFORM" \
  --provenance=false \
  --progress=plain \
  -t "$IMAGE_TAG" \
  -f erun-devops/docker/erun-docs/Dockerfile \
  .
