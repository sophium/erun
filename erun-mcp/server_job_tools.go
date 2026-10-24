package erunmcp

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	activitypkg "github.com/sophium/erun/erun-mcp/tools/activity"
	"github.com/sophium/erun/erun-mcp/tools/agent"
	"github.com/sophium/erun/erun-mcp/tools/aisessions"
	"github.com/sophium/erun/erun-mcp/tools/idle"
	"github.com/sophium/erun/erun-mcp/tools/jobs"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

func registerIdleStopTools(reg shared.ToolRegistrar, runtime shared.RuntimeConfig) {
	shared.AddTool(reg, &mcp.Tool{
		Name:        "idle",
		Description: "Return environment idle stop timeout and marker status without recording activity. Includes the leases currently holding the env busy, so a caller can see what is deferring auto-stop.",
	}, idle.IdleTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name: "activity_lease_take",
		Description: "Hold a named lease for the lifetime of long work in this env, so it reports as busy and idle-stop leaves it alone. " +
			"Take one before detaching a build, test suite, or agent run: a detached job makes no MCP calls while it runs, so without a lease the env reads as untouched and auto-stop would kill exactly the work worth protecting. " +
			"Re-taking the same id renews it. Pass the detached job's pid so the lease is reclaimed if it dies; a lease also expires on its own, so it can never pin the env awake forever. " +
			"Set exclusive=true before any mutating work (git checkout, staging, committing) in a target environment: at most one exclusive holder is allowed per scope (default 'worktree'), so a second exclusive take in that scope is refused and named in the error, while a holder in a different scope - a separate clone in the same pod - is unaffected. An exclusive take is also refused while an operator's own SSH session is active in the environment, since the operator never takes a lease of their own. Which work a held claim refuses depends on its scope: only an 'environment' claim refuses other job starts here, while a 'worktree' claim - the default - is refused by erun exec gate-merge, which rewrites that one shared worktree, and a claim at another scope refuses neither.",
	}, activitypkg.ActivityLeaseTakeTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "activity_lease_release",
		Description: "Release a lease taken by activity_lease_take once the work is done, so the env can go idle again. Releasing an unknown or already-expired lease succeeds, and the result's released field reports whether anything was actually held rather than reading the same success either way. Pass exclusive=true and the same scope to release an exclusive claim; only the id that took it can release it, and if a different id holds that scope the call fails naming the actual holder instead of silently leaving it in place.",
	}, activitypkg.ActivityLeaseReleaseTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "activity_lease_list",
		Description: "List the leases currently holding this env busy. Reading the list also reclaims leases that expired or whose holder process is gone, so what it returns is what is actually deferring auto-stop.",
	}, activitypkg.ActivityLeaseListTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "idle_stop_cancel",
		Description: "Dismiss the pending auto-stop grace warning for the env without touching AWS state. No-op when no warning is armed.",
	}, idleStopCancelTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "idle_stop_history",
		Description: "Return the last N (cap 10) auto-stop audit entries for the env, newest first. Each row carries the per-marker idle/active breakdown captured when the auto-stop grace was armed.",
	}, idleStopHistoryTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "idle_stop_record",
		Description: "Record a host-driven stop entry in stop-history.json (source=host-manual). Called by the desktop's Stop button after the AWS stop succeeds, so the History tab can also explain 'you clicked Stop' alongside the in-pod monitor's auto-stops.",
	}, idleStopRecordTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name: "ai_sessions",
		Description: "Return the structured status (idle, busy, awaiting-input, exited, or oom-killed) of AI tool sessions in this environment, resolved from each session's own last reported turn-boundary event -- never from PTY output volume or silence. " +
			"Pass session to resolve one session; omit it to list every session recorded for the environment. " +
			"The write side is 'erun activity ai-session report', which a tool's own hooks invoke at each turn boundary.",
	}, aisessions.AiSessionsTool(runtime))
}

// registerJobTools registers the exec_job_* family (#1246, following the
// rename #1186 already decided: `job` becomes a sub-family of `exec`) plus
// exec_agent, the job-starting tool that belongs beside them for the same
// reason #1186 gave -- the tool that starts a job and the tools that query it
// should live in one family. job_start itself is gone rather than aliased:
// its command mode and agent mode had incompatible schemas, so a single
// compatibility shim would have meant shipping a tool whose input shape lied
// about what it accepted. Its command mode is exec_raw's wait:false path and
// its agent mode is exec_agent, both already registered elsewhere.
func registerJobTools(reg shared.ToolRegistrar, runtime shared.RuntimeConfig) {
	shared.AddTool(reg, &mcp.Tool{
		Name: "exec_agent",
		Description: "Run an AI tool (claude or codex) as a detached job and return its handle. " +
			"erun invokes it in its streaming mode, so exec_job_output returns events while it works and exec_job_status reports its current activity rather than only running -- running the tool through exec_raw instead would report nothing at all until it exits. " +
			"The job also holds an activity lease for its lifetime, so the env reports as busy and idle-stop leaves it alone. " +
			"Then use exec_job_await (bounded) and exec_job_output (incremental) rather than holding this call open. " +
			"The id defaults to the name; re-using the id of a job that is still running is refused, while re-using a finished one replaces it. " +
			"env sets additional environment for just this run (e.g. raising CLAUDE_CODE_MAX_OUTPUT_TOKENS for a run that will write large files) — see the env parameter description for what it refuses and why it is not for secrets. " +
			"This is a one-shot, non-interactive run: nothing wakes it once it exits, so it must not end its own turn believing something will notify it about work it is still waiting on. If the run itself starts another job through its own Bash tool (a full build, most often) before ending, await that job to a real verdict first -- the wrapping job here does the same internally and will not report exited until it does. There is one narrow exception: if this run's own process ends while what it started has not reached a verdict, or has reached a bad one, erun resumes the same session with the concrete outcome so the model can act on it, bounded to a small fixed count and a wall-clock budget (see exec_job_status's reinvocationCount). Past that bound, or for any other outcome, nothing acts on it -- write the prompt as if there is no later, because past the bound there is none.",
	}, agent.AgentTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name: "exec_job_attach",
		Description: "Give work you started another way a job handle and an activity lease, so it is visible and protected from idle-stop. " +
			"erun tracks the pid you name and nothing else: the job reads as running while that process lives and as unknown once it is gone. " +
			"It can never report an exit status, because nothing erun ran was waiting on that process to observe one — start work through exec_raw or exec_agent when you need the outcome.",
	}, jobs.JobAttachTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name: "exec_job_status",
		Description: "Return one job's state and outcome, or every retained job newest-first. " +
			"The answer is always definite: running, exited with a captured exit code, or explicitly unknown with the reason (its supervisor is gone without an outcome, most often because the runtime pod was replaced). " +
			"It is never a truncated or partial answer, so it is safe to act on: unknown is exactly as terminal as exited — never a reason to keep waiting, and never inferred as 'not finished yet' just because it is not a success. Finished jobs stay readable for 24 hours, so an orchestrator reconnecting after the work ended can still learn what happened. " +
			"A job that itself started another job (a nested exec_agent/exec_raw call, e.g. an agent job running a gate through its own Bash tool) does not report exited the instant its own process ends: it waits for that started job to reach a verdict first, folding a failure into startedJobFailed rather than letting its own clean exit code hide it. abandoned means its own process exited but left other work it started still running unsupervised; gate-incomplete means the started job still had not reached a verdict once the wait was exhausted -- both are as terminal as exited and never a success, whatever the raw exitCode says. This is why an agent job's own process exiting is never the verdict on its own: the one thing worth reading back is this job's own eventual state, not merely that the underlying tool stopped producing output. " +
			"reinvocationCount is how many bounded, automatic follow-up turns exec_agent already ran in response to exactly the gate-incomplete/startedJobFailed case above -- a real second chance for the agent to act on the outcome, resumed on the same session so it carries genuine context rather than a cold restart. It is capped at a small fixed count and a wall-clock budget; a job still gate-incomplete or naming a startedJobFailed once that cap is spent says so explicitly in reason, distinct from a job that never needed one. " +
			"aliveAgeMs is the milliseconds since the supervisor's last ~1s beat, computed by erun in its own clock so a caller never subtracts a pod timestamp from its own — the two clocks are not the same clock. Once it exceeds 5000, treat the job as failed (an unknown outcome, never a success, never a tool error) even if state has not caught up to say so yet; a silent-but-healthy command never trips this, because the beat has nothing to do with the work's own output. " +
			"An agent job also carries progress — current activity (the last tool and its target), turns, tools run, and the last thing the agent said — normalized by erun from the tool's own event stream, so the shape is the same across AI tools. Poll this to report an in-pod agent's progress; do not scrape the agent's private transcript. " +
			"A job started as a background task (build, deploy, doctor, and the rest of the job-envelope tools called with wait: false) carries its typed result on this record's result field once exited, in the same shape the tool would have returned synchronously. " +
			"command is collapsed to a short identifying prefix once it runs past 240 characters -- an agent job's command embeds its whole dispatch prompt, and a status poll is never where that is needed back, since you already have it.",
	}, jobs.JobStatusTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name: "exec_job_await",
		Description: "Wait a bounded time (default 30s, max 600s -- a longer timeoutSeconds is refused, not silently capped) for a job to finish. " +
			"A job that runs longer than 600s (a full build with its test suite routinely does) is not a case this tool refuses to cover: call it again, at up to the 600s max, each time it reports timedOut=true, instead of asking for one longer wait or treating the timeout as the answer -- timedOut=true is never a verdict on the job, only on this one call's own bounded wait. " +
			"The call always returns inside the timeout — either the outcome or timedOut=true with the job still running — so no connection is held open for the work's lifetime and a dropped stream is never confused with a dead job. " +
			"timedOut is reported separately from every outcome, so 'not finished yet' can never be read as a failure — but it is not the only non-outcome case: a job whose supervisor died reads back as state=unknown with timedOut=false, its own third case, distinct from both success and 'still running'. Never re-await a job already reporting unknown expecting a different answer. Call it again only while the job is genuinely still running. " +
			"An agent job's own process exiting is not this call's cue to stop: if that process started another job through its own Bash tool and left it running, this job keeps reading as running (not exited) until that started job reaches a verdict too, up to gate-incomplete if the wait is exhausted first -- including, for an agent job, one bounded automatic resumption of the session to act on that verdict before either is reported final (see exec_job_status's reinvocationCount). Keep awaiting this job's id, not the underlying tool's transcript, for the real answer. " +
			"The returned job's aliveAgeMs (see exec_job_status) is the faster of the two signals: it crosses the 5000ms caller threshold before state necessarily catches up, so a caller in a hurry can act on it directly instead of waiting for the next reconcile. " +
			"command is collapsed the same way exec_job_status collapses it (see there), so a bounded wait that times out on a long-running agent job never re-echoes its whole dispatch prompt.",
	}, jobs.JobAwaitTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name: "exec_job_output",
		Description: "Read a page of a job's captured output, including while it is still running, so progress is visible before the work exits. " +
			"Pass the previous read's nextOffset back as offset to continue where you left off. " +
			"complete is true only when the job has finished and you have read to the end; the job's own outputTruncated says whether output was dropped at the cap. " +
			"The returned job also carries aliveAgeMs (see exec_job_status) — a silent-but-healthy command never advances outputBytes for minutes at a time, so use aliveAgeMs, not output growth, to tell quiet-but-alive apart from actually dead. " +
			"A background task job (see exec_job_status) has no output log of its own; read its typed result off the returned job instead.",
	}, jobs.JobOutputTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name: "exec_job_cancel",
		Description: "Signal a running job's work (TERM by default, or INT/HUP/KILL). " +
			"The target comes from the job record, never from a command-line pattern, so a cancel can only reach the work it names and never a process that merely looks like it. " +
			"The job's supervisor is deliberately left alone, so it survives to record the outcome; the cancelled job then reads back as a normal exited job carrying the signal. " +
			"A background task job (build, deploy, doctor, and the rest) has no subprocess of its own to signal and is refused; wait for it or let it finish.",
	}, jobs.JobCancelTool(runtime))

	// Deprecated aliases for the five renamed job tools, kept callable for one
	// release (the same window #1186 used for the exec_* renames). job_start
	// has no equivalent alias: see the function comment above for why.
	shared.AddTool(reg, &mcp.Tool{
		Name:        "job_attach",
		Description: "Deprecated: use exec_job_attach. Retained for one release; this name will be removed.",
	}, jobs.JobAttachTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "job_status",
		Description: "Deprecated: use exec_job_status. Retained for one release; this name will be removed.",
	}, jobs.JobStatusTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "job_await",
		Description: "Deprecated: use exec_job_await. Retained for one release; this name will be removed.",
	}, jobs.JobAwaitTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "job_output",
		Description: "Deprecated: use exec_job_output. Retained for one release; this name will be removed.",
	}, jobs.JobOutputTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "job_cancel",
		Description: "Deprecated: use exec_job_cancel. Retained for one release; this name will be removed.",
	}, jobs.JobCancelTool(runtime))
}
