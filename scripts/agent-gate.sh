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
# outcome instead. Set AGENT_GATE_RERUN=1 to skip the check and force a new
# run once the underlying work has genuinely changed.

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

await_timeout="${ERUN_AGENT_GATE_AWAIT_TIMEOUT:-8m}"

if [ "${AGENT_GATE_RERUN:-}" != "1" ]; then
	status_status=0
	status_line=$(erun exec job status \
		--tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
		--id "$job_id" 2>/dev/null) || status_status=$?

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
				--id "$job_id" --timeout 1s >&2 || replay_status=$?
			erun exec job output \
				--tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
				--id "$job_id" --max-bytes 16777216
			exit "$replay_status"
			;;
		esac
	fi
fi

start_status=0
start_output=$(erun exec job start \
	--tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
	--id "$job_id" --name "$job_name" \
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
	--id "$job_id" --timeout "$await_timeout" >&2 || await_status=$?

if [ "$await_status" -eq 124 ]; then
	printf 'agent-gate: %s is still running after %s; run this command again to keep waiting\n' "$job_name" "$await_timeout" >&2
	exit 124
fi

erun exec job output \
	--tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
	--id "$job_id" --max-bytes 16777216

exit "$await_status"
