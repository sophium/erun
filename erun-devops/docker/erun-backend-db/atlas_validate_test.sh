#!/bin/sh

# Proves that `atlas migrate validate` genuinely detects the exact defect
# shape that shipped in erun-backend-db:1.0.247: a migration file edited
# after its atlas.sum checksum was generated, with nobody re-running
# `atlas migrate hash` before the change reached main and, from there, a
# published release image. The mismatch sat undetected in the baked
# migrations directory because nothing validated it before publish -- it
# only ever reported "You have a checksum error in your migration directory"
# at deploy time, on every tenant that pinned that version. Root cause,
# confirmed against this repository's own history: the migration's own
# squash-merge commit already carried a wrong per-file hash for
# 20260902130000_gate_runs.sql (unrelated to file content -- the same byte
# content later hashes correctly), and it self-healed on `main` only as an
# incidental side effect of the next migration's author running a fresh
# `atlas migrate hash` for their own change -- a fix that could not reach the
# already-tagged v1.0.247 release.
#
# Needs only the `atlas` CLI -- no postgres, no docker -- run directly (not
# via a container) so it locks the same command `make test-atlas-validate`
# and erun-devops/docker/erun-backend-db/Dockerfile's own build-time RUN
# step both run.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
db_module="$(cd "${script_dir}/../../../erun-backend/erun-backend-db" && pwd)"

command -v atlas >/dev/null 2>&1 || {
    echo "FAIL: atlas is required" >&2
    exit 1
}

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

work_dir="$(mktemp -d)"
trap 'rm -rf "${work_dir}"' EXIT INT TERM

cp -r "${db_module}/migrations/default" "${work_dir}/default"

# --- 1. The real, currently-committed migration directory must validate ---
atlas migrate validate --dir "file://${work_dir}/default" \
    || fail "the repository's own migrations/default must validate cleanly"

# --- 2. Editing a migration file's content without re-hashing must be
#         caught, reproducing the exact v1.0.247 symptom ---
target="${work_dir}/default/20260902130000_gate_runs.sql"
[ -f "${target}" ] || fail "expected fixture migration ${target} to exist"
printf '\n-- edited after hash\n' >>"${target}"

output="$(atlas migrate validate --dir "file://${work_dir}/default" 2>&1)" && \
    fail "expected atlas migrate validate to refuse a migration file edited after its checksum was generated"

echo "${output}" | grep -q "checksum" \
    || fail "expected a checksum-mismatch error, got: ${output}"
echo "${output}" | grep -q "20260902130000_gate_runs.sql" \
    || fail "expected the error to name the edited file, got: ${output}"

echo "OK: atlas migrate validate passes the real migrations directory and catches a file edited after its checksum was generated"
