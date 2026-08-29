---
title: Activities and recovery
---

# Activities and recovery

Every deploy, open, and build the desktop runs lands in the Activities panel, and a failed one carries the evidence and the recovery actions right there instead of sending you to a terminal.

### In-flight and recent operations queue

A queue of recent and in-flight operations — deploys, opens, builds. When a deploy fails, the entry keeps the captured command output (the real helm/kubectl error behind the one-line summary): expand **Show output** to read it, or use **Copy failure report** to package that output together with the environment, version, and container status so you can hand the whole picture to whoever can help. The failed entry also offers one-click recovery: **Run doctor** to troubleshoot (see [`erun doctor`](/cli/doctor)), **Rebuild & redeploy** to force a clean rebuild, and **Clear pending helm release** when a release is stuck.

- **Closing the window while work is running asks first.** If a build, deploy, or release is still in progress when you close ERun, a confirmation names every job that would be cut off before you commit — closing anyway stops them immediately rather than pausing them, so treat a release interrupted mid-flight as one to check (its image or chart may have published without its git tag). Closing with nothing running is unchanged. If you close anyway, the next launch reports what was interrupted so you're not left guessing.

### Investigate, within limits

The same failed entry offers **Investigate**, which hands the failure report to an Agent that reads it and either fixes the problem or improves the reporting behind it. That Agent runs on the account every Agent of yours runs on, so investigations are bounded rather than unlimited, and each refusal says which limit applied and when it lifts: a report with nothing to work from — no command, no exit status, no captured output — is refused, and the missing evidence is named as the thing to fix; further reports from a failure already being investigated join that investigation instead of starting another; the same failure is not investigated again for two hours; at most two run at once; and one that has not concluded within thirty minutes is stopped, which you are told about. While it runs, an investigation holds the environment's activity lease and appears as one of its jobs, so the environment reads as busy and you can see what is spending the account.

## Where next

- [Deploying a version](/desktop/deploying-a-version) — what a deploy actually installs.
- [Diagnostics console](/desktop/diagnostics-console) — the environment's full trace log, for evidence beyond a single failed entry.
- [`erun doctor`](/cli/doctor) — the same diagnosis from the CLI.
