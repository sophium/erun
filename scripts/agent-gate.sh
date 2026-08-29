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
# recorded run. A job id that is stable across invocations (`check`,
# `ui-playwright`, `integration-test`) would otherwise replay a finished
# outcome over code changed after that run -- greening work nobody tested.
# So the id actually used against the job store folds in a hash of the tree
# state (HEAD, working-tree status, and diff content) alongside the caller's
# id: an unchanged tree keeps hitting the same id and replays as before: a
# changed tree resolves to a new id, finds no finished record under it, and
# runs fresh. AGENT_GATE_RERUN=1 still skips the check and forces a new run
# regardless of tree state.

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

resolved_job_id="${job_id}-$(tree_state_key)"

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
		*)
			printf 'agent-gate: %s already finished; reporting its recorded outcome instead of starting a new run (set AGENT_GATE_RERUN=1 to force a fresh run)\n' "$job_name" >&2
			replay_status=0
			erun exec job await \
				--tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
				--id "$resolved_job_id" --timeout 1s >&2 || replay_status=$?
			erun exec job output \
				--tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
				--id "$resolved_job_id" --max-bytes 16777216
			exit "$replay_status"
			;;
		esac
	fi
fi

start_status=0
start_output=$(erun exec job start \
	--tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
	--id "$resolved_job_id" --name "$job_name" \
	--env AGENT_GATE_DETACHED=1 \
	-- "$@" 2>&1) || start_status=$?

if [ "$start_status" -ne 0 ]; then
	case "$start_output" in
	*"is already running"*)
		# Another invocation of this same command already detached the work;
		# fall through and await the job already in flight.
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
