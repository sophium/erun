#!/usr/bin/env sh
# Runs a list of independent shell commands with bounded concurrency,
# buffering each command's combined stdout/stderr so concurrent output never
# interleaves, then emits every buffered block in input order under its own
# marker line and aggregates every failure into one final report -- the same
# contract the serial `for m in ...; do ...; done` loops it replaces had:
# run everything, name every failure, exit non-zero iff anything failed.
#
# Each job is one line on stdin, tab-separated:
#   <short-name>\t<marker-text>\t<command>
# - short-name: bare identifier used in the aggregated failure line
# - marker-text: text printed after ">> " (kept identical to the prior
#   serial recipes' markers so log-grepping and tooling keep working)
# - command: run via `sh -c`
#
# Usage: parallel-gate.sh <max-parallel> <failure-message-prefix>
#
# Exit status is 0 iff every job exits 0. On any failure, prints
# "<failure-message-prefix> failed in:<space-separated short-names>" to
# stderr (matching the exact "lint failed in:..." text the lint recipe
# already produced) and exits 1.
set -eu

max_parallel=$1
prefix=$2

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

tab=$(printf '\t')
i=0
running=0
while IFS="$tab" read -r short_name marker cmd; do
	i=$((i + 1))
	printf '%s' "$short_name" > "$tmp/$i.name"
	printf '%s' "$marker" > "$tmp/$i.marker"
	(
		if sh -c "$cmd" > "$tmp/$i.out" 2>&1; then
			echo 0 > "$tmp/$i.rc"
		else
			echo 1 > "$tmp/$i.rc"
		fi
	) &
	running=$((running + 1))
	if [ "$running" -ge "$max_parallel" ]; then
		wait
		running=0
	fi
done
wait

total=$i
failed=""
j=1
while [ "$j" -le "$total" ]; do
	echo ">> $(cat "$tmp/$j.marker")"
	cat "$tmp/$j.out"
	if [ "$(cat "$tmp/$j.rc")" != "0" ]; then
		failed="$failed $(cat "$tmp/$j.name")"
	fi
	j=$((j + 1))
done

if [ -n "$failed" ]; then
	echo "$prefix failed in:$failed" >&2
	exit 1
fi
