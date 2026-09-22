#!/bin/sh

# Guards the coupling between a built image and the chart default that deploys
# it. `erun-devops/docker/<image>/VERSION` is the pin the build publishes
# under; the charts repeat that tag as a template default. Nothing tied the two
# together, so a bump could build and publish an image no environment ever
# deployed: the dind sidecar's BuildKit GC ceiling shipped in 28.1.1-3 while
# the chart kept defaulting to 28.1.1-2, leaving the fix inert on every
# environment rolled onto that release.
#
# Two checks, because the coupling breaks in two places:
#
#   1. The rendered erun-devops manifest's dind image tag equals
#      docker/erun-dind/VERSION. This reads the manifest the cluster actually
#      receives, so neither stale value plumbing nor a reintroduced literal can
#      slip past it.
#   2. Every image-version literal across the k8s templates equals the
#      docker/<image>/VERSION it mirrors. The dind skew was one instance of a
#      general pattern -- postgres, zitadel, powerdns, and oci-registry repeat
#      their pins the same way -- and this is what keeps all of them agreeing.
#
# Render-only, like the chart tests beside it: no cluster required.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
docker_dir="${repo_root}/erun-devops/docker"
chart_dir="${script_dir}/erun-devops"

command -v helm >/dev/null 2>&1 || {
    echo "FAIL: helm is required to render the runtime chart" >&2
    exit 1
}

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t erun-image-version-test)"
trap 'rm -rf "${work_root}"' EXIT INT TERM

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

# The pinned version of an image, or empty when the repo ships no such image.
pinned_version() {
    file="${docker_dir}/$1/VERSION"
    [ -f "${file}" ] || return 0
    tr -d '[:space:]' <"${file}"
}

# --- 1. The rendered dind sidecar runs the image the repo publishes ---------
want="$(pinned_version erun-dind)"
[ -n "${want}" ] || fail "erun-devops/docker/erun-dind/VERSION is missing or empty"

rendered="${work_root}/render.yaml"
helm template test "${chart_dir}" \
    --set tenant=team \
    --set environment=dev \
    --set worktreeStorage=pvc \
    --set worktreeRepoName=petios >"${rendered}" || fail "helm template failed"

dind_image="$(grep -o 'erun-dind:[^"[:space:]]*' "${rendered}" | head -1)"
[ -n "${dind_image}" ] ||
    fail "the rendered chart carries no erun-dind image, so this test would check nothing"

got="${dind_image##*:}"
[ "${got}" = "${want}" ] ||
    fail "the rendered chart deploys erun-dind:${got} but erun-devops/docker/erun-dind/VERSION is ${want}: the built image would never reach an environment"

# --- 2. Every image-version literal in the templates matches its pin --------
sites="${work_root}/sites.txt"
grep -rh --include='*.yaml' -e '%s/erun-' -e 'ImageTag' "${script_dir}" >"${sites}" || true

pairs="${work_root}/pairs.txt"
# `printf "%s/<image>:<tag>"` -- a literal tag written beside the image name.
# `<registry>/erun-*:<VERSION>` placeholders in prose match nothing here.
sed -n 's/.*%s\/\(erun-[a-z0-9-]*\):\([A-Za-z0-9][^"[:space:]]*\)".*/\1 \2/p' "${sites}" >"${pairs}"
# `default "<tag>" .Values.<image>ImageTag` -- the tag behind an indirection.
sed -n 's/.*default "\([A-Za-z0-9][^"]*\)" \.Values\.\([a-z0-9-]*\)ImageTag.*/erun-\2 \1/p' "${sites}" >>"${pairs}"

lines="$(wc -l <"${pairs}" | tr -d '[:space:]')"
sort -u "${pairs}" -o "${pairs}"

checked=0
while read -r image tag; do
    [ -n "${image}" ] || continue
    pin="$(pinned_version "${image}")"
    [ -n "${pin}" ] || continue
    checked=$((checked + 1))
    [ "${tag}" = "${pin}" ] ||
        fail "a chart template pins ${image}:${tag} but erun-devops/docker/${image}/VERSION is ${pin}"
done <"${pairs}"

[ "${checked}" -gt 0 ] ||
    fail "no image-version literals found in the chart templates; they all moved behind a shared source, so rewrite this test to follow them rather than deleting it"

echo "PASS: image versions agree with their VERSION pins (dind rendered, ${checked} image(s) across ${lines} template line(s))"
