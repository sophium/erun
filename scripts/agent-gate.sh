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
# The environment a run executes under is the third input that decides what
# its result means, and it is deliberately *not* one of the terms in the id
# above. Moving the id with the environment would move it out from under the
# bounded wait's own re-attach path -- a caller re-invoking after an expired
# wait computes the id from its current environment, and if that moved it
# would find no job to re-attach to and try to start a second gate beside the
# first, which the exclusive claim below can only refuse. The id stays stable
# and the *record* carries the environment instead: the name this leaves in
# the job store ends with a marker derived from the environment of the
# invocation that started it, and a recorded pass is replayed only when that
# marker matches the environment asking for it. A differing environment is
# never replayed -- it is named loudly and run fresh, because a pass recorded
# somewhere else answers a question the caller did not ask. A record with no
# marker at all (one written before this existed, still readable for the
# store's retention window) is treated the same way: unattributable, so not
# replayed.
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
#
# A bounded wait timing out (exit 124, "still running") is not this gate's
# verdict, and nothing here may report it as one -- but the exit code that
# means that clearly to a caller checking it directly stops meaning anything
# once `make` is between them and it: GNU Make collapses any nonzero recipe
# exit to its own generic exit 2, so a caller that only sees `make check`'s
# own exit status (an outer job supervisor polling this whole invocation as
# one unit, rather than re-invoking it or querying the named inner job id
# itself) cannot tell a timeout from a real failure -- both read back as the
# same exit 2, and a bounded wait that expired has already been reported as a
# gate failure this way once. The default behaviour for a live,
# foreground-constrained caller (a coding agent's own shell tool call, which
# is exactly what this whole detach mechanism exists to protect, see the file
# header above) must not change to fix that: it cannot safely block past its
# own bounded wait without risking the exact foreground-timeout trap this
# script exists to avoid. `ERUN_JOB_ID` being set is not a safe signal to
# tell the two kinds of caller apart either -- it is set on a coding agent's
# own foreground call just as often as on an asynchronous orchestrator's,
# whenever the agent's own session is itself supervised as a job. So a caller
# that is *not* foreground-constrained -- an external orchestrator polling
# this environment asynchronously over its own MCP edge, a human at a
# terminal, or CI with a generous budget -- opts in explicitly with
# AGENT_GATE_AWAIT_VERDICT=1: this keeps re-awaiting the same job across
# repeated bounded `job await` calls (each still capped the same way) until
# it reaches a real verdict (pass or fail), so `make check`'s own exit status
# is only ever a genuine 0 or a genuine failure -- never a swallowed timeout.
# Without it, behaviour stays foreground-safe: one bounded wait, and 124 if
# that wait ends with no verdict.
#
# A timeout never stands for an outcome on its own. The job's own record is
# read first, so a job that finished either side of the expiry is reported by
# its actual result rather than by the wait that happened to notice first; 124
# is reserved for a job still genuinely running, and an unreadable record
# counts as the same non-verdict rather than as a failure.
#
# This script's own exit code distinguishes four outcomes, not two: 0 for a
# clean pass, 0 (with a named warning on stderr) for a pass that exited 0 but
# left unsupervised background work running behind it, nonzero for a genuine
# gate failure, and 124 -- distinct, and never a failure -- for a wait that
# expired with the job still running and no verdict reached. Only the first
# three survive `make`: GNU Make collapses every nonzero recipe exit to 2, so
# a caller that reads only `make check`'s exit status cannot separate the
# fourth from the third, and must read the job id this script names instead.
# See erun-common/AGENTS.md's "Gate execution and verdicts" for the
# distinction between wrapper status and completed work.

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

# gate_scope_env names the environment variables a gate's *selection* travels
# in -- for the Playwright suite, which areas of it run. They are as much a
# part of what a run means as its argv: `run.sh --skip-app-gates` under
# PLAYWRIGHT_TEST_AREAS=all is a different run from the same command under
# "smoke", and keying them the same would replay one's pass over the other's
# request. That is the same failure the command hash above exists to prevent,
# in the one channel the command hash cannot see.
gate_scope_env="PLAYWRIGHT_TEST_AREAS"

# cmd_state_key prints a key that changes whenever the command being gated
# does, so a job id built from it can never satisfy a request for a
# differently-scoped run (e.g. a full `make check-gate` reusing a narrower
# run's cached result under the same job id).
cmd_state_key() {
	{
		printf '%s\0' "$@"
		for name in $gate_scope_env; do
			# printenv, not an indirect expansion of the shell variable, so the
			# key reflects the environment the gate will actually run under:
			# a value that never reached this process cannot scope a run it is
			# not in.
			value=$(printenv "$name" 2>/dev/null) || value=""
			printf '%s\0%s\0' "$name" "$value"
		done
	} | hash_stdin | cut -c1-16
}

# gate_control_env names the variables *this wrapper itself* consumes to decide
# how it waits and whether it replays at all -- never anything the gated
# command can read to change what it does. They are held out of the
# environment key below so that a caller turning one on for a retry (adding
# AGENT_GATE_AWAIT_VERDICT=1 to its second invocation, say) does not read as a
# different run and cost it the record it came back to collect.
gate_control_env="AGENT_GATE_RERUN AGENT_GATE_AWAIT_VERDICT ERUN_AGENT_GATE_AWAIT_TIMEOUT"

# env_state_key prints a key over the environment a gated run will execute
# under: the *whole* environment, minus exactly the wrapper controls above --
# not a list of variables known to matter. Which variables a gate reads is not
# knowable from here (a starvation knob, a timeout, a feature flag, a database
# URL -- whatever the suite being gated happens to consult), and a list of the
# ones known today silently omits the next one, which is precisely the failure
# this key exists to prevent, arriving as an omission instead of a mistake. So
# a difference is the default and the exclusion is the narrow, reviewable
# part. Like the scope key above, it reads the process environment rather than
# the shell's own variables, so a value that never reached this process
# cannot describe a run it is not in.
env_state_key() {
	pattern=""
	sep=""
	for name in $gate_control_env; do
		pattern="${pattern}${sep}^${name}="
		sep="|"
	done
	env | grep -v -E "$pattern" | LC_ALL=C sort | hash_stdin | cut -c1-16
}

# recorded_env_key prints the environment marker a recorded job's own status
# line carries, and nothing when it carries none -- a record written before
# this existed, or a line this version cannot read. The last match wins: the
# marker is appended after whatever name the caller supplied, so a name that
# happened to contain one cannot displace the real one.
recorded_env_key() {
	printf '%s\n' "$1" | sed -n 's/.*\[env \([0-9a-f]\{16\}\)\].*/\1/p'
}

resolved_job_id="${job_id}-$(tree_state_key)-$(cmd_state_key "$@")"

# The environment this invocation runs under, and the name the job store will
# record for it. The marker rides in the name because the name is the one
# field `erun exec job status` reports back on its own line; the id cannot
# carry it without moving the re-attach path the header describes.
env_key=$(env_state_key)
record_name="$job_name [env $env_key]"

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
			# no special-casing is needed here. There is also nothing else this
			# invocation could usefully do -- starting a second gate beside this one
			# is what the exclusive claim below refuses -- so a run started under a
			# different environment is attached to and reported, but never silently:
			# its verdict is that environment's, and a caller that takes it for this
			# one's would be recording a result it never produced.
			running_env=$(recorded_env_key "$status_line")
			if [ -n "$running_env" ] && [ "$running_env" != "$env_key" ]; then
				printf 'agent-gate: WARNING -- %s is already running as job %s, started under environment %s, not the environment this invocation is under (%s). The verdict reported below will be that run'\''s, for that environment -- not one this invocation asked for. Wait for it to finish, then run this command again to get a verdict for this environment.\n' "$job_name" "$resolved_job_id" "$running_env" "$env_key" >&2
			fi
			;;
		"exited 0:"*)
			replayed_env=$(recorded_env_key "$status_line")
			if [ "$replayed_env" != "$env_key" ]; then
				# A pass is only ever evidence for the run that produced it, and the
				# environment is part of what that run was. Replaying it here would
				# assert this configuration passed without ever executing it -- the
				# caller is told a run happened that did not. So fall through and
				# actually run it, which is also the only honest answer available:
				# the recorded pass is real, it is simply not this run's.
				if [ -z "$replayed_env" ]; then
					env_mismatch="its record carries no environment marker (it predates this check), so it cannot be attributed to any environment"
				else
					env_mismatch="it ran under environment $replayed_env, and this invocation is under $env_key"
				fi
				printf 'agent-gate: refusing to replay the cached PASS for %s from job %s -- %s. A pass recorded under one environment is not a pass under another, so %s runs fresh instead.\n' "$job_name" "$resolved_job_id" "$env_mismatch" "$job_name" >&2
			else
				printf 'agent-gate: replaying a cached PASS for %s from job %s (%s) -- tree, command and environment unchanged since that run; set AGENT_GATE_RERUN=1 to force a fresh run\n' "$job_name" "$resolved_job_id" "$status_line" >&2
				replay_status=0
				erun exec job await \
					--tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
					--id "$resolved_job_id" --timeout 1s >&2 || replay_status=$?
				erun exec job output \
					--tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
					--id "$resolved_job_id" --max-bytes 16777216
				exit "$replay_status"
			fi
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
	--id "$resolved_job_id" --name "$record_name" \
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

while :; do
	await_status=0
	erun exec job await \
		--tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
		--id "$resolved_job_id" --timeout "$await_timeout" >&2 || await_status=$?

	if [ "$await_status" -ne 124 ]; then
		break
	fi

	# 124 is this wait's own deadline, not the job's outcome, and the two are
	# independent: the job can reach its verdict either side of the moment the
	# wait gives up. So read the job's own record before letting the expiry
	# stand for anything -- a job that has since reached an outcome is reported
	# by that outcome below, and only a job still genuinely running is a
	# non-verdict. An unreadable record is the same non-verdict: an unknown
	# outcome is never a failure.
	late_status_line=$(erun exec job status \
		--tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
		--id "$resolved_job_id" 2>/dev/null) || late_status_line=""

	case "$late_status_line" in
	running:* | "")
		if [ "${AGENT_GATE_AWAIT_VERDICT:-}" != "1" ]; then
			printf 'agent-gate: %s has reached no verdict -- job %s is still running after %s. This is NOT a failure of the gated work: run this command again to re-attach to the same job and keep waiting.\n' "$job_name" "$resolved_job_id" "$await_timeout" >&2
			exit 124
		fi

		printf 'agent-gate: %s has reached no verdict -- job %s is still running after %s; AGENT_GATE_AWAIT_VERDICT=1 is set, so waiting again for a real verdict instead of reporting the bounded wait itself as a failure\n' "$job_name" "$resolved_job_id" "$await_timeout" >&2
		;;
	*)
		# The job finished after all. Report its own outcome, never the
		# expired wait that happened to notice first.
		case "$late_status_line" in
		"exited 0:"*)
			await_status=0
			;;
		*)
			await_status=1
			;;
		esac
		break
		;;
	esac
done

# `job await`'s own exit status collapses two different outcomes into the same
# nonzero code: a genuinely failed gate, and a gate that exited 0 but left
# unsupervised work running behind it (state "abandoned" -- see
# environmentJobSucceeded in erun-common/job.go, which treats both as
# "not Succeeded"). The job record itself still tells them apart, so re-read
# it once here instead of propagating that collapse into this script's own
# exit code. A real orphan still matters -- it means work this gate started
# never reached its own verdict -- so it is surfaced as a loud, named warning,
# never silently. See erun-common/AGENTS.md's "Gate execution and verdicts".
final_status="$await_status"
if [ "$await_status" -ne 0 ]; then
	final_status_line=$(erun exec job status \
		--tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
		--id "$resolved_job_id" 2>/dev/null) || final_status_line=""
	case "$final_status_line" in
	"abandoned 0:"*)
		printf 'agent-gate: WARNING -- %s exited 0 but left background work running behind it: %s -- treating this as PASS, not a failure, but that leftover work never reached a verdict of its own and still needs investigating.\n' "$job_name" "$final_status_line" >&2
		final_status=0
		;;
	esac
fi

erun exec job output \
	--tenant "$ERUN_TENANT" --environment "$ERUN_ENVIRONMENT" \
	--id "$resolved_job_id" --max-bytes 16777216

exit "$final_status"
