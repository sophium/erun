#!/bin/sh
# Install or refresh baked reusable agents into an agent's agents directory.
#
# ERun bakes canonical reusable agents under /etc/erun/agents/<name>.md
# (vendored from erun-skills/agents/). This mirrors skills-install.sh's
# install/refresh/preserve policy exactly, adapted for a flat file instead of a
# skill directory: an env's home persists on a PVC, so an agent installed once
# must still track a rebuilt image's updated copy, unless the operator edited
# it in-pod, which is preserved.
#
# Provenance is tracked per agent in a sidecar marker
# (<name>.md.erun-agent-baked-sha256, alongside the installed file rather than
# inside it since there is no directory to hold it): an installed copy whose
# content still matches its marker is unmodified since erun installed it and
# is safe to refresh; one that differs was edited in-pod and is left
# untouched. A legacy copy with no marker (installed before markers existed)
# is treated as unmodified and refreshed to the baked version.
#
# Usage: erun-install-agents <baked_root> <dest_agents_root>
#   e.g. erun-install-agents /etc/erun/agents "${HOME}/.claude/agents"
#
# Source with ERUN_AGENTS_INSTALL_LIB=1 to load the functions without running.
set -eu

MARKER_SUFFIX=".erun-agent-baked-sha256"

# agent_sha prints the sha256 of a file, or nothing when the file is absent.
# Prefers sha256sum (the Linux runtime image); falls back to shasum so the test
# suite runs on a macOS dev host too.
agent_sha() {
    [ -f "$1" ] || return 0
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | cut -d' ' -f1
    else
        shasum -a 256 "$1" | cut -d' ' -f1
    fi
}

# copy_agent replaces dst_file with a fresh copy of src_file and records the
# baked hash in its sidecar marker so a later run can tell an untouched copy
# from an edited one.
copy_agent() {
    src_file="$1"
    dst_file="$2"
    cp "${src_file}" "${dst_file}"
    chmod 0644 "${dst_file}"
    agent_sha "${src_file}" >"${dst_file}${MARKER_SUFFIX}"
}

install_or_refresh_agent() {
    src_file="$1"                       # /etc/erun/agents/<name>.md
    dst_file="$2"                       # <dest_root>/<name>.md
    marker="${dst_file}${MARKER_SUFFIX}"

    if [ ! -e "${dst_file}" ]; then
        copy_agent "${src_file}" "${dst_file}"
        return 0
    fi

    baked_sha="$(agent_sha "${src_file}")"
    inst_sha="$(agent_sha "${dst_file}")"
    marker_sha=""
    if [ -f "${marker}" ]; then
        marker_sha="$(cat "${marker}")"
    fi

    if [ "${inst_sha}" = "${baked_sha}" ]; then
        # Already the baked version; keep the marker in sync (also adopts a
        # pre-marker copy that happens to match, so later upgrades refresh it).
        printf '%s\n' "${baked_sha}" >"${marker}"
        return 0
    fi

    if [ -z "${marker_sha}" ] || [ "${inst_sha}" = "${marker_sha}" ]; then
        # Unmodified since erun installed it (marker matches), or a legacy
        # copy with no marker: the operator has not edited it, so refresh.
        copy_agent "${src_file}" "${dst_file}"
        return 0
    fi

    # inst differs from both baked and its recorded marker → edited in-pod.
    # Leave it untouched so operator edits survive.
    return 0
}

install_or_refresh_agents() {
    baked_root="$1"
    dest_root="$2"
    [ -d "${baked_root}" ] || return 0
    mkdir -p "${dest_root}"
    for src_file in "${baked_root}"/*.md; do
        [ -f "${src_file}" ] || continue
        agent_name="$(basename "${src_file}")"
        install_or_refresh_agent "${src_file}" "${dest_root}/${agent_name}"
    done
}

# Run when executed directly; skip when sourced by the test suite.
if [ "${ERUN_AGENTS_INSTALL_LIB:-}" != "1" ]; then
    if [ "$#" -ne 2 ]; then
        echo "usage: erun-install-agents <baked_root> <dest_agents_root>" >&2
        exit 2
    fi
    install_or_refresh_agents "$1" "$2"
fi
