#!/usr/bin/env sh
# Runs a long gate command directly, unless this process is inside an erun
# agent environment's own pod (ERUN_ENV_TYPE local-agent or remote-agent) --
# exactly the case where the caller is an in-pod coding agent whose own
# harness auto-backgrounds a foreground command once it outruns its
# foreground window, leaving the agent holding a bare task handle it has to
# poll one turn at a time instead of a result.
#
# There, this detaches the real work through erun's own job primitive
# instead: `erun exec job start` runs it independently of this process, and
# `erun exec job await` blocks for a bounded window and returns either the
# finished outcome or a "still running" timeout. Whichever way the caller
# invokes this script -- once and re-invoked later, or left to time out and
# re-invoked again -- the cost is a small, bounded number of calls rather than
# one per poll interval.
#
# Usage: agent-gate.sh <job-id> <job-name> -- <command...>
#
# Outside an agent pod (a human's terminal, CI, a plain `docker build`, or
# when AGENT_GATE_DETACHED=1 marks this as the job's own re-exec'd body)
# the command just runs in place via exec, so behaviour and exit status are
# unchanged from calling it directly.
#
# `erun exec job start` replaces a finished record under the same id rather
# than refusing to reuse it, so that an orchestrator can re-run named work
# without inventing ids -- but re-invoking this wrapper after the job it
# started has already finished is exactly the case the doc comment above
# tells a caller to do ("run this command again to keep waiting"). Calling
# start unconditionally would discard the outcome the caller came back to
# collect and start a fresh run in its place, so before starting anything
# this checks whether the job already finished and, if so, reports that
# outcome instead.
#
# That replay is only safe when the tree being gated hasn't moved since the
# recorded run, and when the command being run is the same one that produced
# the recorded outcome. A job id that is stable across invocations (`check`,
# `ui-playwright`, `integration-test`) would otherwise replay a finished
# outcome over code changed after that run, or a full run's id over a
# narrower one's result -- greening work nobody tested, or answering a
# request for the full suite with a focused run's verdict. So the id
# actually used against the job store folds in a hash of the tree state
# (HEAD, working-tree status, and diff content) and a hash of the exact
# command being gated, alongside the caller's id: an unchanged tree and
# command keep hitting the same id and replay as before; either one changing
# resolves to a new id, finds no finished record under it, and runs fresh.
# AGENT_GATE_RERUN=1 still skips the check and forces a new run regardless of
# tree state.
#
# A replayed result is never silently indistinguishable from a fresh one: it
# is always announced on stderr, naming the job id it came from (queryable
# again with `erun exec job status`) and the exact recorded outcome. And a
# replay only ever stands in for another *passing* run -- a recorded failure,
# an abandoned job, or anything else short of a clean exit is never replayed,
# since a stale failure is cheap to re-check and a stale record of anything
# other than success has no value. Only a stale pass could plausibly be
# mistaken for a real one, and only for that case does the tree+command key
# above have to carry the whole safety burden.

set -eu

job_id=$1
job_name=$2
shift 2
if [ "${1:-}" = "--" ]; then
	shift
fi

is_agent_pod() {
	[ "${ERUN_ENV_TYPE:-}" = "local-agent" ] || [ "${ERUN_ENV_TYPE:-}" = "remote-agent" ]
}

if [ "${AGENT_GATE_DETACHED:-}" = "1" ] || ! is_agent_pod || ! command -v erun >/dev/null 2>&1; then
	exec "$@"
fi

: "${ERUN_TENANT:?agent-gate.sh: ERUN_TENANT is not set (expected inside an agent pod)}"
: "${ERUN_ENVIRONMENT:?agent-gate.sh: ERUN_ENVIRONMENT is not set (expected inside an agent pod)}"

# warn_if_wrapped_in_timeout looks a few hops up the process tree for an
# ancestor named `timeout`. An outer `timeout` around this script can only
# ever truncate the bounded await below -- it has no way to extend it -- so
# wrapping a call meant to protect a gate in a second, shorter deadline can
# only defeat the protection, never help it. This is a warning, not a refusal:
# the ancestor walk is a `ps` heuristic (best-effort if `ps` is unavailable),
# and a caller who genuinely wants a hard outer bound has a real use for it
# (e.g. bounding total wall-clock across several agent-gate.sh re-invocations).
warn_if_wrapped_in_timeout() {
	pid="$PPID"
	hops=0
	while [ -n "$pid" ] && [ "$pid" != "0" ] && [ "$pid" != "1" ] && [ "$hops" -lt 8 ]; do
		comm=$(ps -o comm= -p "$pid" 2>/dev/null | tr -d ' ') || return 0
		case "$comm" in
		timeout|*/timeout)
			printf 'agent-gate: this invocation appears to run under an outer '\''timeout'\'' (pid %s) -- timeout can only truncate the bounded await below, never extend it, so it can end up killing the gate this script exists to protect. Run agent-gate.sh directly in the foreground and re-invoke it again if it reports "still running" instead.\n' "$pid" >&2
			return 0
			;;
		esac
		pid=$(ps -o ppid= -p "$pid" 2>/dev/null | tr -d ' ')
		hops=$((hops + 1))
	done
}
warn_if_wrapped_in_timeout

# hash_stdin prints the sha256 of stdin. Prefers sha256sum (the Linux runtime
# image); falls back to shasum so this runs on a macOS dev host too.
hash_stdin() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum | cut -d' ' -f1
	else
		shasum -a 256 | cut -d' ' -f1
	fi
}

# tree_state_key prints a key that changes whenever the working tree does:
# the commit, the status of every tracked/untracked path, and the actual
# diff content against HEAD (status alone can't tell two different edits to
# the same path apart, since both show as the same "M <path>" line). Falls
# back to a fixed marker outside a git worktree, which keeps replay keyed on
# job id alone there -- the same as before this existed.
tree_state_key() {
	if ! command -v git >/dev/null 2>&1 || ! git rev-parse --git-dir >/dev/null 2>&1; then
		printf 'no-git'
		return
	fi
	{
		git rev-parse HEAD
		git status --porcelain
		git diff HEAD
	} 2>/dev/null | hash_stdin | cut -c1-16
}

# cmd_state_key prints a key that changes whenever the command being gated
# does, so a job id built from it can never satisfy a request for a
# differently-scoped run (e.g. a full `make check-gate` reusing a narrower
# run's cached result under the same job id).
cmd_state_key() {
	printf '%s\0' "$@" | hash_stdin | cut -c1-16
}

resolved_job_id="${job_id}-$(tree_state_key)-$(cmd_state_key "$@")"

await_timeout="${ERUN_AGENT_GATE_AWAIT_TIMEOUT:-8m}"

if [ "${AGENT_GATE_RERUN:-}" != "1" ]; then
	status_status=0
	status_line=$(erun exec job status \
		--tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
		--id "$resolved_job_id" 2>/dev/null) || status_status=$?

	if [ "$status_status" -eq 0 ]; then
		case "$status_line" in
		running:*)
			# Still running: the start/await/output flow below already attaches to
			# it correctly (start reports "already running" and falls through), so
			# no special-casing is needed here.
			;;
		"exited 0:"*)
			printf 'agent-gate: replaying a cached PASS for %s from job %s (%s) -- tree and command unchanged since that run; set AGENT_GATE_RERUN=1 to force a fresh run\n' "$job_name" "$resolved_job_id" "$status_line" >&2
			replay_status=0
			erun exec job await \
				--tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
				--id "$resolved_job_id" --timeout 1s >&2 || replay_status=$?
			erun exec job output \
				--tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
				--id "$resolved_job_id" --max-bytes 16777216
			exit "$replay_status"
			;;
		*)
			# A recorded outcome exists but is not a clean pass (a failure, an
			# abandoned job, an incomplete gate, or anything else). Never replay
			# it -- a stale non-pass has no value and re-running it is cheap --
			# so fall through to start a genuinely fresh run instead.
			printf 'agent-gate: %s has a non-passing recorded result from job %s (%s); running fresh instead of replaying it\n' "$job_name" "$resolved_job_id" "$status_line" >&2
			;;
		esac
	fi
fi

# --exclusive is what makes a gate's verdict mean something. A gate saturates
# the pod, so anything scheduled beside it changes the answer rather than just
# the duration: the same gate measured GREEN at 7m4s/7m38s/6m58s alone, and
# GREEN 17m36s / RED / RED with a second gate batch and probe jobs sharing a
# 12-CPU pod -- both reds on tests that pass standalone, one of them an
# `erun usage --output json` golden whose actual output carried real OOM
# warnings. A contended gate does not report a slow verdict, it reports a
# wrong one, so the claim is taken rather than the contention merely
# documented. Nested work this gate itself starts runs under the same claim
# and is not refused by it.
#
# This script runs against whichever `erun` the pod has installed, which it
# does not control and which is routinely a release or two behind the checkout
# being gated -- so support for the flag is read off the installed binary's
# own help rather than assumed. Passing an unknown flag would fail every start
# outright, turning a protection into an outage of the gate itself on exactly
# the environments that have not upgraded yet. Degrading loudly is the right
# trade: an unprotected gate is what those environments already had.
exclusive_flag=""
if erun exec job start --help 2>/dev/null | grep -q -- '--exclusive'; then
	exclusive_flag="--exclusive"
else
	printf 'agent-gate: this environment'\''s erun predates `job start --exclusive`, so %s runs without claiming the environment. Make sure nothing else is scheduled here while it runs -- a contended gate reports a wrong verdict, not a slow one. Upgrade the environment (erun pin / erun deploy) to have the claim enforced for you.\n' "$job_name" >&2
fi

start_status=0
start_output=$(erun exec job start \
	--tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
	--id "$resolved_job_id" --name "$job_name" \
	${exclusive_flag:+"$exclusive_flag"} \
	--env AGENT_GATE_DETACHED=1 \
	-- "$@" 2>&1) || start_status=$?

if [ "$start_status" -ne 0 ]; then
	case "$start_output" in
	*"is already running"*)
		# Another invocation of this same command already detached the work;
		# fall through and await the job already in flight.
		;;
	*"held exclusively"*)
		# Something else holds this environment. Report the refusal verbatim --
		# it names the holder and how to reach it -- and exit non-zero rather
		# than awaiting a job that was never started, which would otherwise
		# read as this gate's own failure.
		printf '%s\n' "$start_output" >&2
		printf 'agent-gate: %s was not started -- this environment is held by other work. Wait for the holder above to finish and re-invoke this command; running the gate beside it would report a wrong verdict, not just a slow one.\n' "$job_name" >&2
		exit "$start_status"
		;;
	*)
		printf '%s\n' "$start_output" >&2
		exit "$start_status"
		;;
	esac
else
	printf '%s\n' "$start_output" >&2
fi

await_status=0
erun exec job await \
	--tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
	--id "$resolved_job_id" --timeout "$await_timeout" >&2 || await_status=$?

if [ "$await_status" -eq 124 ]; then
	printf 'agent-gate: %s is still running after %s; run this command again to keep waiting\n' "$job_name" "$await_timeout" >&2
	exit 124
fi

erun exec job output \
	--tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
	--id "$resolved_job_id" --max-bytes 16777216

exit "$await_status"
