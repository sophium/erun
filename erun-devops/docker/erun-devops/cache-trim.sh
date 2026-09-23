#!/bin/sh

# cache-trim bounds a cache directory by *size*, evicting the entries that have
# gone longest unused rather than clearing the directory.
#
# Go already bounds its own build cache by age. cmd/go/internal/cache trims
# entries whose mtime is older than five days, at most once a day, and refreshes
# an entry's mtime on use (at most hourly) so those mtimes really do mean "last
# used". That is a recency rule with no ceiling, and the five-day working set of
# a multi-module repository built in several variants -- per GOARCH, with and
# without -race, test binaries beside compiled packages -- reaches tens of
# gigabytes on a busy environment. The build cache lands under ~/.cache on the
# home volume and nothing capped it, so several environments on one node grew it
# until the node hit DiskPressure and stopped scheduling all of them.
#
# Clearing the cache bounds it too, and is what makes every later build cold --
# a cost this exists to avoid. The eviction here keeps the signal Go already
# maintains, last-use mtime, so the entries a build is about to want are exactly
# the ones that survive; what goes is the bulk that nothing has touched since it
# was written.
#
# Only Go's own cache entries are candidates: names ending in -a or -d, the same
# set cmd/go's trimSubdir removes. The cache's bookkeeping beside them
# (trim.txt, README, log.txt) is left alone, and trim.txt is deliberately not
# rewritten -- this is not the age trim, and claiming a trim just ran would
# suppress the one Go does on its own for the next day.
#
# An entry written while a trim runs is not protected. A file removed between
# one build writing it and the next build reading it is a cache miss, which the
# go command already tolerates; it is never made to fail.

set -eu

usage() {
    echo "usage: erun-trim-cache [--dry-run] <dir> <max-bytes>" >&2
    exit 2
}

dry_run=false
if [ "${1:-}" = "--dry-run" ]; then
    dry_run=true
    shift
fi

dir="${1:-}"
max_bytes="${2:-}"

[ -n "${dir}" ] || usage
case "${max_bytes}" in
    '' | *[!0-9]*) usage ;;
esac
[ "${max_bytes}" -gt 0 ] || usage

# Absent is not empty: a cache directory that does not exist holds nothing, and
# creating it here would leave a build to find a directory it never made.
[ -d "${dir}" ] || exit 0

# du, not a find -printf sum: an executable cache entry is a directory, and find
# reports a directory's own size rather than what it holds. The eviction pass
# below has to use find -- it needs each entry's mtime beside its size -- so it
# under-counts those, which is why that pass is repeated against this reading
# rather than trusted to have landed on the cap in one go.
dir_size_bytes() {
    du -sb "$1" 2>/dev/null | awk 'NR == 1 { print $1; exit }'
}

# evict_lru <dir> <bytes-to-free> prints how many entries it removed (or, under
# --dry-run, how many it would). Entries are taken oldest-mtime first and the
# sum stops as soon as it covers what has to go, so only the evicted slice is
# ever held and a trim that has to free a little removes a little.
evict_lru() {
    plan="$(mktemp)"

    find "$1" -mindepth 2 -maxdepth 2 \( -name '*-a' -o -name '*-d' \) \
        -printf '%T@\t%s\t%p\n' 2>/dev/null |
        sort -k1,1n |
        awk -F'\t' -v want="$2" '
            { total += $2; print $3 }
            total >= want { exit }
        ' >"${plan}"

    count=0
    if [ -s "${plan}" ]; then
        count="$(wc -l <"${plan}" | tr -d ' ')"
        if [ "${dry_run}" = true ]; then
            rm -f "${plan}"
            printf '%s\n' "${count}"
            return 0
        fi
        xargs -r -d '\n' rm -rf -- <"${plan}"
    fi
    rm -f "${plan}"
    printf '%s\n' "${count}"
}

before="$(dir_size_bytes "${dir}")"
[ -n "${before}" ] || exit 0
[ "${before}" -gt "${max_bytes}" ] || exit 0

if [ "${dry_run}" = true ]; then
    would_evict="$(evict_lru "${dir}" "$(( before - max_bytes ))")"
    printf 'erun: %s bytes of go build cache at %s are over the %s byte cap; %s least recently used entries would be evicted (--dry-run, nothing removed)\n' \
        "${before}" "${dir}" "${max_bytes}" "${would_evict}" >&2
    exit 0
fi

evicted=0
attempt=0
# The pass under-counts executable entries (see dir_size_bytes), so it is
# repeated against a fresh reading rather than assumed to have reached the cap.
# Four passes is slack for that under-count, not a retry loop: a pass that frees
# nothing ends it immediately, and a cache still over its cap afterwards is
# reported instead of being ground down further.
while [ "${attempt}" -lt 4 ]; do
    now="$(dir_size_bytes "${dir}")"
    if [ -z "${now}" ] || [ "${now}" -le "${max_bytes}" ]; then
        break
    fi
    evicted=$((evicted + $(evict_lru "${dir}" "$((now - max_bytes))")))
    attempt=$((attempt + 1))
done

after="$(dir_size_bytes "${dir}")"
printf 'erun: trimmed the go build cache at %s from %s to %s bytes, evicting %s least recently used entries against a %s byte cap\n' \
    "${dir}" "${before}" "${after}" "${evicted}" "${max_bytes}" >&2

if [ -n "${after}" ] && [ "${after}" -gt "${max_bytes}" ]; then
    printf 'erun: the go build cache at %s is still %s bytes over its %s byte cap after %s passes; its remaining entries are not the ones this bound can free\n' \
        "${dir}" "${after}" "${max_bytes}" "${attempt}" >&2
fi
