#!/bin/sh
# Install or refresh baked agent skills into an agent's skills directory.
#
# ERun bakes canonical skills under /etc/erun/skills/<name>/ (vendored from
# erun-skills/). An env's home persists on a PVC, so a skill installed once used
# to be frozen forever (the old `[ ! -e ]` guard), meaning a rebuilt image's
# updated skill never reached existing envs. This installs a skill when absent
# AND refreshes it when the baked copy changed — unless the operator edited it
# in-pod, which is preserved.
#
# Provenance is tracked per skill by recording the baked SKILL.md hash in a
# marker (.erun-skill-baked-sha256): an installed copy whose SKILL.md still
# matches its marker is unmodified since erun installed it and is safe to
# refresh; one that differs was edited in-pod and is left untouched. A legacy
# copy with no marker (installed before markers existed) is treated as
# unmodified and refreshed to the baked version.
#
# Usage: erun-install-skills <baked_root> <dest_skills_root>
#   e.g. erun-install-skills /etc/erun/skills "${HOME}/.claude/skills"
#
# Source with ERUN_SKILLS_INSTALL_LIB=1 to load the functions without running.
set -eu

MARKER_NAME=".erun-skill-baked-sha256"

# skill_sha prints the sha256 of a file, or nothing when the file is absent.
# Prefers sha256sum (the Linux runtime image); falls back to shasum so the test
# suite runs on a macOS dev host too.
skill_sha() {
    [ -f "$1" ] || return 0
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | cut -d' ' -f1
    else
        shasum -a 256 "$1" | cut -d' ' -f1
    fi
}

# copy_skill replaces dst_dir with a fresh copy of src_dir and records the baked
# SKILL.md hash so a later run can tell an untouched copy from an edited one.
copy_skill() {
    src_dir="$1"
    dst_dir="$2"
    rm -rf "${dst_dir}"
    mkdir -p "${dst_dir}"
    cp -R "${src_dir}." "${dst_dir}/"
    find "${dst_dir}" -type f -exec chmod 0644 {} +
    skill_sha "${src_dir}SKILL.md" >"${dst_dir}/${MARKER_NAME}"
}

install_or_refresh_skill() {
    src_dir="$1"                        # /etc/erun/skills/<name>/ (trailing slash)
    dst_dir="$2"                        # <dest_root>/<name>
    baked_md="${src_dir}SKILL.md"
    inst_md="${dst_dir}/SKILL.md"
    marker="${dst_dir}/${MARKER_NAME}"

    # A baked directory with no SKILL.md is not a skill; skip it.
    [ -f "${baked_md}" ] || return 0

    if [ ! -e "${inst_md}" ]; then
        copy_skill "${src_dir}" "${dst_dir}"
        return 0
    fi

    baked_sha="$(skill_sha "${baked_md}")"
    inst_sha="$(skill_sha "${inst_md}")"
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
        # Unmodified since erun installed it (marker matches), or a legacy copy
        # with no marker: the operator has not edited it, so refresh to baked.
        copy_skill "${src_dir}" "${dst_dir}"
        return 0
    fi

    # inst differs from both baked and its recorded marker → edited in-pod.
    # Leave it untouched so operator edits survive.
    return 0
}

install_or_refresh_skills() {
    baked_root="$1"
    dest_root="$2"
    [ -d "${baked_root}" ] || return 0
    for src_dir in "${baked_root}"/*/; do
        [ -d "${src_dir}" ] || continue
        skill_name="$(basename "${src_dir}")"
        install_or_refresh_skill "${src_dir}" "${dest_root}/${skill_name}"
    done
}

# Run when executed directly; skip when sourced by the test suite.
if [ "${ERUN_SKILLS_INSTALL_LIB:-}" != "1" ]; then
    if [ "$#" -ne 2 ]; then
        echo "usage: erun-install-skills <baked_root> <dest_skills_root>" >&2
        exit 2
    fi
    install_or_refresh_skills "$1" "$2"
fi
