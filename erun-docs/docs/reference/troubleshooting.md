---
title: Troubleshooting
---

# Troubleshooting

Common failures and how to diagnose them. The first thing to reach for in any unclear state is **`erun doctor`** — it inspects the local config and the runtime pod and offers recovery actions for everything it knows how to fix.

## `erun open` hangs at "waiting for SSH"

**Symptoms:** the command sits indefinitely after `helm upgrade` completes.

**Diagnose:**

```bash
# From another shell:
erun list                                       # confirm the env's status
kubectl get pods -n <tenant>-<env>              # is the runtime pod actually up?
kubectl logs -n <tenant>-<env> <pod> -c erun-devops --tail=200
```

**Common causes:**

- The runtime image is pulling for the first time on a slow connection — wait a minute, retry.
- The env's SSH endpoint is disabled — re-enable it in the desktop's env settings.
- The cluster's `binfmt` init container failed (multi-arch images can't be unpacked). `kubectl logs … -c binfmt` will show why.

## `erun build` fails with "unauthorized" / "denied"

**Symptoms:** `docker push` rejects with HTTP 401 / 403.

**Fix path:**

1. `erun push` will retry automatically with an interactive `docker login`. If you're not in a TTY, that retry is skipped — run `docker login <registry>` once by hand and retry.
2. For GHCR, the token needs `write:packages`. `erun push` tries to widen it automatically with `gh auth refresh -s write:packages,read:packages`, but that needs an interactive browser login — so it's skipped in CI, over MCP, and inside a runtime pod (the desktop terminal is a pod shell with no browser). When it's skipped, the push fails with the exact recovery commands. Run them from a host shell with a browser: `gh auth refresh -h github.com -u <owner> -s write:packages,read:packages` then `gh auth token -u <owner> -h github.com | docker login ghcr.io -u <owner> --password-stdin`.
3. For ECR, the token expires after 12 hours. `aws ecr get-login-password --region <r> | docker login --username AWS --password-stdin <account>.dkr.ecr.<r>.amazonaws.com`.

## `erun build` fails at a download, minutes in, on a healthy network {#build-network-mtu}

**Symptoms:** a build step that fetches something over HTTPS stalls and then fails — `curl: (35) Recv failure: Connection reset by peer`, `curl: (28) Connection timed out`, an `apt`/`apk` fetch failure — while the same URL fetches fine from a shell in the same environment. It often looks architecture-specific, because the failure lands on whichever platform iteration happened to be in flight.

**Cause:** the environment's docker daemon is bridging build containers at a larger MTU than the pod network can carry. A cluster CNI that encapsulates gives the pod less than docker's 1500 default (a VXLAN overlay leaves 1450), so the oversized replies are dropped and nothing signals it back. Small packets pass, which is why the connection opens and a TLS handshake gets partway through before stopping dead.

**Fix path:**

1. Confirm it: `erun build` prints a `warning:` naming both MTUs at the start of a build when they disagree, and a build that then fails on the network says so in the error and in its `~/.erun/timing/build-*.json` record.
2. `erun deploy <tenant> <environment>` — the runtime chart derives the daemon's MTU from the pod network, so redeploying is the fix. An environment deployed before that behaviour existed keeps the old sidecar until it is redeployed.

## "kubernetes context not found"

**Symptoms:** any command that touches the cluster aborts with this message.

**Fix:**

```bash
kubectl config get-contexts                     # what's actually configured?
erun list                                       # what does the env expect?
```

If the expected context is missing, restore your `~/.kube/config` from the cloud-provider tool (`aws eks update-kubeconfig`, `gcloud container clusters get-credentials`, etc.). For a managed ERun cloud context, `erun list cloud` will show the status and `erun open` reissues the kubeconfig automatically.

## Agent can't reach MCP

**Symptoms:** Claude Code / Codex shows "MCP server not reachable" or hangs on `tools/list`.

**Diagnose:**

```bash
# From your laptop:
cat <UserConfigDir>/erun/portforward/mcp/<tenant>/<env>.json
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:<localPort>/mcp
```

A `200` (or any non-zero response) means the server is up. If the call hangs or 404s, the port-forward died — `erun open` re-establishes it. For the JSON-RPC handshake an Agent uses against the same endpoint, see [MCP overview · Worked example](/mcp/overview#worked-example).

Replacing the runtime pod breaks the forward in one of two ways, and neither announces itself. Usually `kubectl port-forward` exits along with the pod, so the local port is simply free — `lsof` shows nothing, which from the outside is indistinguishable from an environment nobody ever opened. Occasionally the listener outlives its far end instead, so `lsof` still shows it and only a real request reveals the failure.

The desktop app watches for both, and re-runs the reconnect itself a few times before giving up. What tells it apart from an environment nobody opened is the recorded forward above: `erun open` writes that file, so an environment without one is never touched, and an environment you stopped is never woken. When the desktop gives up it says so: the environment's sidebar row turns to a warning triangle reading **unreachable**, and a notification names the port and which of the two faults it found. That is the point to deploy the environment, because a forward a fresh `erun open` cannot fix is a runtime problem rather than a tunnel one.

## Connecting a laptop-side Agent client

The Agent runs inside the env by default — the runtime pod ships the `EnvConfig.aitool` CLI pre-wired to MCP loopback, and the desktop's AI panel attaches to it. If you nevertheless want to drive an env from an Agent running on your laptop (debugging the in-pod Agent, scripting across envs, one-off testing), use this:

1. Open the env so the port-forward exists:
   ```bash
   erun open my-tenant local
   ```
2. Configure the client to launch `erun mcp proxy` as a stdio MCP server for that env. Example for Claude Code:
   ```bash
   claude --mcp-config '{"mcpServers":{"my-tenant-local":{"type":"stdio","command":"erun",
     "args":["mcp","proxy","--tenant","my-tenant","--environment","local"]}}}'
   ```
   For Codex / custom clients, use the equivalent stdio-server entry with the same command and arguments. See [`erun mcp` · Wiring a laptop-side MCP client](/cli/mcp#wiring-a-laptop-side-mcp-client).
3. Verify by asking the Agent to call the `list` tool — it should return the same data as `erun list`.

If you find yourself reaching for this regularly, treat it as a signal that the in-pod Agent's config or version needs work — `erun doctor` will surface most causes.

**"requires re-authorization (token expired)".** This means the client was configured with a bearer of its own rather than with `erun mcp proxy`. Bearers are short-lived by design, a client reads its config once at launch and cannot refresh a header, so roughly five minutes in every tool for that env stops at the same moment. Re-point the client at the proxy as shown above: it mints a bearer per request, so nothing in the client's config expires and the session keeps working for as long as it runs.

Meanwhile, and for checking the edge itself:

```bash
erun mcp tools --tenant my-tenant --environment local        # is the edge healthy?
erun mcp call --tool list --output json                      # keep working meanwhile
```

If `erun mcp call` reports `MCP endpoint rejected the bearer token`, the env does not trust this machine at all — redeploy it from the desktop app. If it reports `MCP endpoint is not reachable`, the port-forward is down: re-run `erun open`. The proxy surfaces both of those as JSON-RPC errors carrying the same recovery text, so a client wired through it shows the fix instead of going silent.

## Orchestrator started without its environment tools

**Symptoms:** the desktop titlebar's warning icon picks up an unread message reading `<name> started without its environment tools`, and the [orchestrator](/collaboration/workflow) has none of the tools for the environments it links — every call against one fails, even though the session itself looks healthy.

The desktop wires each linked environment in by launching `erun mcp proxy` for it. When it cannot wire any of them the session still starts, and the warning names which of the two causes applies and offers the matching action right on its row in the [message centre](/desktop/diagnostics-console#message-centre) — open the warning icon and act on it there, no need to leave the app:

- **"the erun executable could not be resolved"** — the `erun` command line tool was not found beside the desktop app or on `PATH`. The message's **Install docs** button opens the CLI install page; once it's installed, click **Restart** to relaunch the orchestrator with its tools wired.
- **"no linked environment resolved an MCP port"** — none of the linked environments resolves a port any more, usually because they were renamed or removed. Check the orchestrator's linked environments in the desktop app, then click **Restart**.

The message stays counted as unread until you open it, and it stays in the dialog's history for the rest of the session either way — plus a copy action, so it's still readable and shareable long after the session comes up. An orchestrator that links no environments has nothing to wire and stays quiet.

## Cloud context won't start

**Symptoms:** `erun open` reports a long-running "starting" status that never resolves.

**Diagnose:**

```bash
erun list cloud                                 # see the resolved status + provider
kubectl --context <cloud-context> get nodes     # is the cluster reachable directly?
```

**Common causes:**

- Cloud account quota — `aws ec2 describe-account-attributes` (or equivalent) shows the limit.
- IAM credentials expired on the host (`aws sso login`).
- Network policy blocks the cluster's API endpoint from your laptop's IP.

## AWS calls in an environment fail with `ExpiredToken` or `no basic auth credentials` {#host-credentials-expired}

**Symptoms:** an environment that worked yesterday now fails every AWS call. The shape of the failure depends on which tool reached AWS first, so it rarely names credentials:

```
$ aws sts get-caller-identity
An error occurred (ExpiredToken) … The security token included in the request is expired

# or, several layers away, from a test suite pulling a container image:
org.testcontainers.containers.ContainerFetchException: Can't get Docker image …
Error response from daemon: Head "https://<account>.dkr.ecr.<region>.amazonaws.com/v2/…":
  no basic auth credentials
```

**Diagnose:**

```bash
erun doctor <tenant> <env>       # reports the erun-host profile's presence and expiry
```

**Cause:** the environment acts as your AWS identity through short-lived credentials in the pod's `erun-host` profile, and they lapsed. The file is still present and well-formed, which is why this reads like a registry or repository problem.

**Fix:**

```bash
erun cloud login --alias <alias>          # only if your local SSO session has also lapsed
erun cloud refresh <tenant> <env>         # re-inject, nothing secret passes through the caller
```

`erun open` performs the same refresh, so reopening the environment also fixes it. See [Acting as your AWS identity](/deployment/cloud-setup#host-credentials).

## Runtime pod stuck in `ImagePullBackOff`, image and chart pull from different registries {#runtime-image-registry-mismatch}

**Symptoms:** the runtime pod's container shows `ImagePullBackOff` / `ErrImagePull` even though the helm release itself installed fine, and `kubectl describe pod` names a registry you don't recognize as the failing pull.

**Diagnose:**

```bash
erun doctor <tenant> <env>       # names both registries when they disagree
```

**Cause:** the environment's `runtimeimage` names a registry other than its `runtimeregistry` — for example a runtime image built into ECR while the chart itself resolves from `ghcr.io`. `erun deploy` refreshes a pull-secret credential for each registry in play independently, so a credential missing for the image's own registry — not the chart's — is what leaves the pod unable to pull.

**Fix:** either confirm a credential for the image's registry resolves where `erun deploy`/`erun open --deploy` runs (the same AWS/docker session that can push to it), or realign the two so `runtimeregistry` matches the image:

```bash
erun init <tenant> <env> --runtime-registry <image's registry>
```

See [`erun doctor`](/cli/doctor) and [Configuration · private image pull secrets](/reference/configuration#advanced-image-pull-secrets).

## `erun deploy` refuses: image is not anonymously pullable {#image-not-anonymously-pullable}

**Symptoms:** `erun deploy` (or `erun init`, `erun open --deploy`, `erun upgrade`) exits non-zero immediately, before any `helm upgrade` or `kubectl` line appears in its output, with an error like:

```
deploy <tenant>-devops: ghcr.io/sophium/<tenant>-devops:<version> is not anonymously pullable and no ghcr.io credential resolved to provision a pull secret -- refusing before the rollout recreates the running pod
```

**Cause:** the runtime image is a private `ghcr.io` package — most commonly your own `<tenant>-devops` umbrella image — and nowhere `erun deploy` looked (your local docker session, a `gh` session, `GH_TOKEN`/`GITHUB_TOKEN`) resolved a credential for it. The runtime chart's rollout strategy is `Recreate`: it tears down the running pod before scheduling the replacement, so an image that can't be pulled would take the environment down rather than leave a stalled rollout beside a healthy pod. `erun deploy` checks this **before** touching the cluster instead of finding out mid-rollout — see [Configuration · the runtime image is checked automatically](/reference/configuration#advanced-image-pull-secrets-preflight).

**Fix:** authenticate wherever `erun deploy` runs, the same as for any other `ghcr.io` push/pull:

```bash
gh auth login -h github.com -s write:packages,read:packages
# or
docker login ghcr.io
```

Then re-run the deploy — once a credential resolves, `erun deploy` auto-provisions and attaches the pull secret itself; no `--image-pull-secret` needed. If the image is not actually private, make the `ghcr.io/sophium/<name>` package public instead.

## `erun deploy` refuses: runtime image release line mismatch {#runtime-image-line-mismatch}

**Symptoms:** `erun deploy` exits non-zero immediately, before any `helm upgrade` or `kubectl` line appears in its output, with an error like:

```
deploy: runtime image ghcr.io/sophium/erun-devops:1.0.86 is on the erun release line, but <tenant>/<env>'s last confirmed deploy ran ghcr.io/sophium/<tenant>-devops:1.0.86 (the <tenant> line) -- pass --runtime-image or --runtime-chart to move release lines on purpose; if runtimeimage config just drifted, `erun doctor` explains how to realign it
```

**Cause:** the environment's last confirmed deploy ran a runtime image on one release line (most commonly the tenant's own `<tenant>-devops` line), but this deploy resolved an image on a *different* line — most often because the persisted `runtimeimage` still names the stock `erun-devops` image from before the environment moved onto its own line (see [Configuration · runtime image and version are one coordinate](/reference/configuration#advanced-runtime-image-line)). The version number alone can't reveal this: `erun-devops` and `<tenant>-devops` can both publish the exact same version number, so the wrong image resolves to a real, existing tag rather than failing outright. Because the runtime chart's rollout strategy is `Recreate`, installing the wrong image would tear down the running (correct) pod first — `erun deploy` refuses before touching the cluster instead.

**Fix:** if you mean to move the environment to a different release line right now, say so explicitly:

```bash
erun deploy <tenant> <env> --version <version> --runtime-image <registry>/<image>
# or
erun deploy <tenant> <env> --version <version> --runtime-chart <chart-reference>
```

If you don't — the pairing just drifted — realign the persisted `runtimeimage` the same way, or run `erun doctor <tenant> <env>` first to confirm which line the environment is actually on (it flags the same mismatch from config alone, no cluster access needed):

```bash
erun doctor <tenant> <env>
```

## An agent environment can fetch but can't push a branch or use `gh` {#git-push-access}

**Symptoms:** a remote-agent (or runtime) environment clones, builds, and tests fine — `make check` or the equivalent gate goes green — but the very last step of the work fails:

```
$ gh auth status
You are not logged into any GitHub hosts. To log in, run: gh auth login

$ git push -u origin my-branch
fatal: could not read Username for 'https://github.com': No such device or address
```

If an agent tries to fix this itself by running `gh auth login`, it can burn many minutes stuck in gh's interactive device-code flow (a code, a URL, a 900-second poll) with nobody able to open the browser to complete it, then exit having done no work.

**Diagnose:**

```bash
erun doctor <tenant> <env>       # reports fetch and push as independent verdicts
```

**Cause:** a public GitHub repository fetches anonymously, so cloning and reading succeed with no credential at all — the environment looks completely healthy for the entire life of a piece of work. Pushing (and using `gh` for anything — reading an issue, opening a PR) needs a credential, and a newly created remote-agent environment has none: no `gh` session, no `GH_TOKEN`/`GITHUB_TOKEN`, no SSH key the remote host accepts. Provisioning this is deliberately manual, the same as an operator's AWS identity, but for a full git/gh identity rather than a narrowly-scoped token — nothing copies a live credential into the pod automatically.

**Fix:** from an interactive shell opened with `erun open <tenant> <env>`, authenticate once:

```bash
gh auth login -h github.com
# or, to skip gh's browser flow entirely:
gh auth login -h github.com --with-token < token-file
```

Run this only from that interactive shell — never from an unattended agent run, which cannot complete gh's device-code/browser flow. The credential persists on the environment's home volume across restarts, so this is a one-time setup per environment. See [`erun doctor` · git push access](/cli/doctor#what-it-checks).

## AWS calls fail with `Invalid endpoint: https://sts..amazonaws.com` {#aws-region-empty}

**Symptoms:** every AWS call in the environment fails with an endpoint that has an empty region in it, and passing `--region <region>` by hand makes the same call succeed.

**Diagnose:**

```bash
erun doctor <tenant> <env>       # the Region line reports what resolved, or that nothing did
```

**Cause:** no AWS region resolved for the environment. ERun resolves one from its managed cloud context, its kubeconfig context name, the alias's Identity Center region, or an ECR registry host — an environment on a local cluster whose alias records none of those has nothing to resolve. ERun deliberately exports **no** `AWS_REGION` in that case rather than an empty one, because an empty value overrides the region an AWS profile would otherwise supply instead of falling back to it.

**Fix:** give the environment a region to find — set the Identity Center region on the alias (`erun cloud init aws --sso-region <region>`), point the environment at an ECR registry, or set `AWS_REGION` yourself in the pod. Then `erun cloud refresh <tenant> <env>` writes the resolved region into the pod's profile and the next deploy exports it.

## Idle-stop fires while you're working

**Symptoms:** the cloud context shuts down even though you're typing.

**Diagnose:**

```jsonc
// Via MCP:
{ "method": "tools/call", "params": { "name": "idle", "arguments": {} } }
```

The response shows `eligible_for_stop` and the activity windows. If `eligible_for_stop` is true while you're working, one of three things is wrong:

- The terminal-input tracker isn't seeing activity — it only watches the runtime pod's tty. If your editor only writes files (no SSH session), terminal activity is zero. Open a shell session alongside the editor.
- The traffic-window byte count is below the env's threshold — lower the threshold from the desktop's env settings.
- You're outside the env's working hours. Adjust the window or unset it.

For the predicate, the activity sources, and the configuration field names, see [Agent reference · Idle policy](/agent-reference/idle-policy).

## `helm upgrade` failed mid-deploy

**Symptoms:** `erun deploy` reports a chart in a failed state after a partial roll-out.

**Recover:**

```bash
helm history <component> -n <tenant>-<env>      # see what happened
helm rollback <component> <revision> -n <tenant>-<env>   # back out
# Then fix and rerun:
erun deploy <component> --dry-run               # preview
erun deploy <component>                          # commit
```

The rest of the deploy plan (steps before and after) is unaffected — `erun deploy` stopped at the failing step and didn't continue.

When you deploy from the desktop app, the [Activities panel](/desktop/activities-and-recovery) keeps the captured output behind the failed entry: **Show output** to read the error inline, or **Copy failure report** to send the full context (output, environment, version, container status) to whoever can help.

## `erun deploy` timed out waiting for the rollout

**Symptoms:** `erun deploy` reports a chart that "never converged" / a rollout timeout, and the new pods showed `Pulling image (ImagePullBackOff)` the whole time.

This is usually the **first deploy of a fresh image tag onto a cold node** — a large runtime image can take several minutes to pull, and the deploy gives up only when the rollout timeout elapses. Nothing was wrong with the image; the pull just outlasted the wait. The fix is to give the pull more room:

```bash
erun deploy <tenant> <env> --version <v> --rollout-timeout 10m   # one-off
```

To make it the default for an environment whose images are consistently large, set `deploy.timeout` in the env's config (see [Configuration · `EnvConfig`](/reference/configuration#envconfig)). A retried deploy is also faster once the image is cached on the node.

`erun deploy` distinguishes a slow pull from a real failure: it **keeps waiting** while a container is still pulling (including `ImagePullBackOff` retries against a slow registry), and **stops early** — `deploy failed early: pod … container … <reason>` — only on a real failure (a crash loop, a bad config, or a permanent image-pull rejection like a missing tag or denied credentials). If you see the early-fail message, the image or container is genuinely broken; fix the cause (push the image, fix the config, check registry credentials) and rerun. The full decision rules are in [Agent reference · Rollout wait and pod monitoring](/agent-reference/cli-flags#rollout-wait-and-pod-monitoring).

## When all else fails

- `erun doctor` from your laptop: reports local config issues.
- `erun open <tenant> <env>` then `erun doctor` from inside the pod: reports in-pod issues.
- `kubectl describe pod -n <tenant>-<env> <pod>`: kubernetes events for the runtime pod.
- Check the [audit trail](/collaboration/operator-in-the-loop#the-audit-trail) for the last successful action — the answer is often one step before that.
