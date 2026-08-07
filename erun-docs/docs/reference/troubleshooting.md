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

When you deploy from the desktop app, the [Activities panel](/desktop/overview#control-panel) keeps the captured output behind the failed entry: **Show output** to read the error inline, or **Copy failure report** to send the full context (output, environment, version, container status) to whoever can help.

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
