#!/bin/sh

# Tests for cache-trim.sh: the go build cache is bounded by size, not cleared.
#
# The properties that matter are the two halves of what the bound is for. It has
# to hold: a workload that keeps adding entries must not keep growing the cache,
# whatever the age of what it adds. And it has to keep the cache warm: the
# entries a just-run build left behind are the ones the next build asks for, so
# an eviction that reached them would turn a bounded cache into a cold one and
# pay the cost this exists to avoid.
#
# Sibling to session-prune_test.sh / worktree-adopt_test.sh: a real baked script
# exercised as a subprocess, not sourced and mocked. The warm-cache half is
# checked against a real `go` toolchain and a real module rather than against a
# fixture that merely looks like a cache, because "the second build reused
# something" is a claim only the go command can make.

set -eu

script_dir="$(cd "$(dirname "$0")" && pwd)"
trim="${script_dir}/cache-trim.sh"
if [ ! -x "${trim}" ]; then
    chmod +x "${trim}"
fi

command -v go >/dev/null 2>&1 || {
    echo "FAIL: go is required to run the warm-cache case" >&2
    exit 1
}

work_root="$(mktemp -d 2>/dev/null || mktemp -d -t cache-trim-test)"
trap 'rm -rf "${work_root}"' EXIT INT TERM

fail() {
    echo "FAIL: $1" >&2
    exit 1
}

# Each case gets its own cache directory, so a case that leaves entries behind
# cannot make the next one's reading depend on it.
case_dir=""
new_case() {
    case_dir="${work_root}/$1"
    mkdir -p "${case_dir}"
}

# add_entry <cache-dir> <subdir> <index> <bytes> <age-hours>
# One go cache entry: a subdirectory named by two hex digits holding a data
# file and its tiny action file, both named by the entry's hash. That is the
# layout cmd/go/internal/cache writes, and the one its own trim walks.
# Names are prefixed because the shell has no locals and these are called from
# inside loops that use the same short names.
add_entry() {
    entry_sub="$2"
    entry_hash="$(printf '%064x' $(( $3 )))"
    mkdir -p "$1/${entry_sub}"
    head -c "$4" /dev/zero >"$1/${entry_sub}/${entry_hash}-d"
    printf 'v1 %s\n' "${entry_hash}" >"$1/${entry_sub}/${entry_hash}-a"
    touch -d "-$5 hours" "$1/${entry_sub}/${entry_hash}-a" "$1/${entry_sub}/${entry_hash}-d"
    touch -d "-$5 hours" "$1/${entry_sub}"
}

# add_entries <cache-dir> <count> <bytes-each> <age-hours> <first-index>
add_entries() {
    i=0
    while [ "${i}" -lt "$2" ]; do
        # Spread across the 256 subdirectories the real cache uses, so the trim
        # is never able to work from one directory's size alone.
        add_entry "$1" "$(printf '%02x' $(( (i + $5) % 256 )))" "$((i + $5))" "$3" "$4"
        i=$((i + 1))
    done
}

size_bytes() {
    du -sb "$1" 2>/dev/null | awk 'NR == 1 { print $1; exit }'
}

has_entry() {
    [ -f "$1/$2/$(printf '%064x' "$3")-d" ]
}

count_entries() {
    find "$1" -mindepth 2 -maxdepth 2 -name '*-d' -type f | wc -l | tr -d ' '
}

# --- 1. A cache inside its cap is left completely alone ---
# The common case by far, and the one that decides whether this is cheap enough
# to run on an interval: it must not rewrite, reorder or report anything.
new_case quiet
add_entries "${case_dir}" 16 4096 1 0
cap=$(( $(size_bytes "${case_dir}") + 1048576 ))
before="$(find "${case_dir}" -mindepth 2 -maxdepth 2 | sort)"
out="$("${trim}" "${case_dir}" "${cap}" 2>&1)"
[ -z "${out}" ] ||
    fail "a cache under its cap should be silent, got: ${out}"
after="$(find "${case_dir}" -mindepth 2 -maxdepth 2 | sort)"
[ "${before}" = "${after}" ] ||
    fail "a cache under its cap should be untouched"

# A directory that does not exist holds nothing. Creating it would leave a
# build to find a cache directory it never made.
out="$("${trim}" "${work_root}/never-created" "${cap}" 2>&1)" ||
    fail "a missing cache directory should not be an error"
[ -d "${work_root}/never-created" ] &&
    fail "a missing cache directory should not be created"
[ -z "${out}" ] || fail "a missing cache directory should be silent, got: ${out}"

# --- 2. Over the cap, the oldest entries go and the newest stay ---
# The whole point of evicting rather than clearing. Ten subdirectories aged from
# oldest to newest, a cap that has room for roughly the newer half of them.
new_case lru
sub=0
while [ "${sub}" -lt 10 ]; do
    add_entry "${case_dir}" "$(printf '%02x' "${sub}")" "$((sub * 100))" 200000 "$(( 400 - sub * 30 ))"
    sub=$((sub + 1))
done
full="$(size_bytes "${case_dir}")"
cap=$(( full / 2 ))
"${trim}" "${case_dir}" "${cap}" 2>/dev/null >/dev/null
bounded="$(size_bytes "${case_dir}")"
[ "${bounded}" -le "${cap}" ] ||
    fail "the cache should be within its ${cap} byte cap after a trim, got ${bounded}"

has_entry "${case_dir}" 00 $((0 * 100)) &&
    fail "the oldest entry in the cache should have been evicted"
has_entry "${case_dir}" 09 $((9 * 100)) ||
    fail "the newest entry in the cache should have survived the trim"

# --- 3. Only go's own entries are candidates ---
# The cache's bookkeeping sits beside the entries and is not oversized content:
# removing trim.txt would discard go's own record of when it last trimmed, and
# removing README/log.txt loses the cache's own documentation and history. A
# file in a subdirectory that is not an entry is nobody's to delete either.
new_case bookkeeping
add_entries "${case_dir}" 8 200000 300 0
printf '200\n' >"${case_dir}/trim.txt"
printf 'docs\n' >"${case_dir}/README"
printf 'history\n' >"${case_dir}/log.txt"
printf 'stray\n' >"${case_dir}/00/not-an-entry"
new_case_keep="${case_dir}"
"${trim}" "${case_dir}" 100000 2>/dev/null >/dev/null
for f in trim.txt README log.txt 00/not-an-entry; do
    [ -e "${new_case_keep}/${f}" ] ||
        fail "the trim removed ${f}, which is not a cache entry"
done

# --- 4. An executable cache entry goes with its contents ---
# An action whose output has to be run is stored as a directory, not a file.
# Those are the entries a per-entry size reading under-counts, so they are the
# ones that could leave the cache over its cap if the pass were trusted once.
new_case execentry
mkdir -p "${case_dir}/aa"
hash="$(printf '%064x' 7)"
mkdir -p "${case_dir}/aa/${hash}-d"
head -c 400000 /dev/zero >"${case_dir}/aa/${hash}-d/exe"
printf 'v1\n' >"${case_dir}/aa/${hash}-a"
touch -d '-300 hours' "${case_dir}/aa/${hash}-a"
touch -d '-300 hours' "${case_dir}/aa/${hash}-d"
add_entries "${case_dir}" 4 50000 1 900
"${trim}" "${case_dir}" 200000 2>/dev/null >/dev/null
bounded="$(size_bytes "${case_dir}")"
[ "${bounded}" -le 200000 ] ||
    fail "an over-cap cache of executable entries should be brought within its cap, got ${bounded}"
[ -e "${case_dir}/aa/${hash}-d" ] &&
    fail "an evicted executable entry should take its directory with it"

# --- 5. --dry-run removes nothing ---
new_case dryrun
add_entries "${case_dir}" 16 200000 300 0
full="$(size_bytes "${case_dir}")"
out="$("${trim}" --dry-run "${case_dir}" $(( full / 4 )) 2>&1)"
[ "$(size_bytes "${case_dir}")" = "${full}" ] ||
    fail "--dry-run removed something"
printf '%s' "${out}" | grep -q 'nothing removed' ||
    fail "--dry-run should say it removed nothing, got: ${out}"

# --- 6. Bad arguments are refused, not guessed at ---
for args in "" "${case_dir}" "${case_dir} zero" "${case_dir} 0" "${case_dir} -5"; do
    # shellcheck disable=SC2086
    if "${trim}" ${args} >/dev/null 2>&1; then
        fail "erun-trim-cache should refuse its arguments: '${args}'"
    fi
done

# --- 7. Bounded under a workload that keeps growing ---
# The property the issue is about: growth must not accumulate. Each round adds
# as much as the cap holds, so a trim that only ever removed the newest entries
# -- or removed nothing -- would be over its cap at the end of the round.
new_case growth
cap=2000000
round=0
while [ "${round}" -lt 6 ]; do
    add_entries "${case_dir}" 24 400000 1 "$(( round * 24 ))"
    "${trim}" "${case_dir}" "${cap}" 2>/dev/null >/dev/null
    current="$(size_bytes "${case_dir}")"
    [ "${current}" -le "${cap}" ] ||
        fail "round ${round}: the cache grew past its ${cap} byte cap to ${current}"
    round=$((round + 1))
done
[ "$(count_entries "${case_dir}")" -gt 0 ] ||
    fail "a bounded cache should still be holding entries, not be emptied"

# --- 8. A bounded cache is still a warm one ---
# Against a real toolchain and a real module: build the cache, bound it, and
# check that the go command still answers from it. The synthetic entries added
# to force the cap are aged, which is the state the bound exists for -- the bulk
# of a grown cache is what nothing has touched since it was written, and the
# working set is what the last build used.
new_case warm
module="${case_dir}/module"
mkdir -p "${module}/alpha" "${module}/beta"
cat >"${module}/go.mod" <<'EOF'
module example.com/warm

go 1.21
EOF
for pkg in alpha beta; do
    cat >"${module}/${pkg}/${pkg}.go" <<EOF
package ${pkg}

func Value() int { return 42 }
EOF
    cat >"${module}/${pkg}/${pkg}_test.go" <<EOF
package ${pkg}

import "testing"

func TestValue(t *testing.T) {
	if Value() != 42 {
		t.Fatal("unexpected")
	}
}
EOF
done

cache="${case_dir}/go-cache"
mkdir -p "${cache}"
# A real module needs somewhere real to write its module cache; keeping it in
# the same directory tree keeps the case self-contained.
export GOPATH="${case_dir}/gopath"
export GOFLAGS=-mod=mod
run_tests() {
    (cd "${module}" && GOCACHE="${cache}" GO111MODULE=on go test ./... 2>&1)
}

run_tests >/dev/null || fail "the sample module's tests should pass"

second="$(run_tests)"
printf '%s' "${second}" | grep -q '(cached)' ||
    fail "a second run of an unchanged module should be served from the cache, got: ${second}"

# The entries the run above just made are the ones a third run wants. Everything
# else added here is aged well past them, so a trim that keeps the working set
# keeps exactly these.
add_entries "${cache}" 40 300000 200 5000
hot="$(size_bytes "${cache}")"
cap=$(( hot / 2 ))
"${trim}" "${cache}" "${cap}" 2>/dev/null >/dev/null
bounded="$(size_bytes "${cache}")"
[ "${bounded}" -le "${cap}" ] ||
    fail "the real cache should be within its ${cap} byte cap after a trim, got ${bounded}"

third="$(run_tests)"
printf '%s' "${third}" | grep -q '(cached)' ||
    fail "bounding the cache must not make the next build cold: the go command stopped answering from it (${third})"

echo "PASS: erun-trim-cache bounds the go build cache by size and keeps it warm"
