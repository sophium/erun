package erunmcp

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-mcp/tools/build"
	"github.com/sophium/erun/erun-mcp/tools/contribute"
	"github.com/sophium/erun/erun-mcp/tools/deploy"
	"github.com/sophium/erun/erun-mcp/tools/diff"
	"github.com/sophium/erun/erun-mcp/tools/doctor"
	"github.com/sophium/erun/erun-mcp/tools/e2e"
	"github.com/sophium/erun/erun-mcp/tools/envdelete"
	"github.com/sophium/erun/erun-mcp/tools/exectool"
	"github.com/sophium/erun/erun-mcp/tools/expose"
	"github.com/sophium/erun/erun-mcp/tools/initenv"
	"github.com/sophium/erun/erun-mcp/tools/jobs"
	"github.com/sophium/erun/erun-mcp/tools/observe"
	"github.com/sophium/erun/erun-mcp/tools/outputs"
	"github.com/sophium/erun/erun-mcp/tools/pin"
	"github.com/sophium/erun/erun-mcp/tools/resize"
	"github.com/sophium/erun/erun-mcp/tools/review"
	"github.com/sophium/erun/erun-mcp/tools/services"
	"github.com/sophium/erun/erun-mcp/tools/shared"
	"github.com/sophium/erun/erun-mcp/tools/terraform"
	"github.com/sophium/erun/erun-mcp/tools/upgrade"
	"github.com/sophium/erun/erun-mcp/tools/usage"
)

func registerReviewTools(reg shared.ToolRegistrar, runtime shared.RuntimeConfig) {
	shared.AddTool(reg, &mcp.Tool{
		Name:        "review_list",
		Description: "List reviews on the erun platform, narrowed by any combination of targetBranch, sourceBranch, status, authorUserId, and reviewerUserId. mine and waitingOnMe resolve to the caller's own user id via whoami and cannot be combined with the equivalent explicit id filter. Supports preview.",
	}, review.ReviewListTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "review_show",
		Description: "Fetch one review on the erun platform together with its comment threads and recorded builds. Supports preview.",
	}, review.ReviewShowTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "review_create",
		Description: "Open a review on the erun platform. name is the eventual squash-merge message and must be unique per tenant; a colliding name fails with a conflict. sourceBranch must already exist on the remote (push it first with exec_push), since the review references it by name and the platform can only ever fetch what has actually landed there. issueRef records the issue the work belongs to, as owner/repo#number or as a bare number joined to the recorded repository; a recorded issue is declared, and outranks the number the source branch names. A real, immediate write, not a preview, unless preview is set.",
	}, review.ReviewCreateTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "review_comment",
		Description: "Comment on a line of a review on the erun platform, or reply to an existing comment with parentCommentId. A real, immediate write, not a preview, unless preview is set.",
	}, review.ReviewCommentTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "review_resolve",
		Description: "Resolve a comment thread on a review by closing its root comment. commentId must be the thread's root comment (the first comment posted at a file/line, not one created with parentCommentId set); addressing a reply fails, naming the root comment to retry against. A real, immediate write, not a preview, unless preview is set.",
	}, review.ReviewResolveTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "review_unresolve",
		Description: "Reopen a comment thread on a review by marking its root comment OPEN again. commentId must be the thread's root comment (the first comment posted at a file/line, not one created with parentCommentId set); addressing a reply fails, naming the root comment to retry against. A real, immediate write, not a preview, unless preview is set.",
	}, review.ReviewUnresolveTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "review_close",
		Description: "Close a review on the erun platform without merging it. A real, immediate write, not a preview, unless preview is set.",
	}, review.ReviewCloseTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "review_record-build",
		Description: "Record a build against a review on the erun platform. This is the only way to transition a review off OPEN: a successful build moves it to READY (and on to MERGE if it was already the merge queue's head), a failed one moves it to FAILED. There is no separate tool to set a review's status directly. commitId must be the full 40-character commit hash the build ran against, and version the version it minted, from the run's own build result (a plain erun build --output json, or --dry-run --output json when it failed before printing one) — required even when successful is false; a recorded build publishes nothing, so this is metadata no platform path resolves and a --release produced version is accepted but not required. The merge queue records its build the same way: the environment a review's merge queue promoted to MERGE runs a plain build of the prospective merge commit exec_gate-merge prepared and records it here with the version it minted; only a successful build of the merge commit that landed can later be reported MERGED. A build reported as failed whose failureDetail names a known erun infrastructure failure (a registry or the network giving up, e.g. a ghcr.io TLS handshake timeout) is refused outright — a build has no INCONCLUSIVE result, so recording it FAILED would move the review out of the merge queue for a network blip; re-run the build once the signature clears and record that result instead. A real, immediate write, not a preview, unless preview is set.",
	}, review.ReviewRecordBuildTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "review_report-merged",
		Description: "Report a review MERGED on the erun platform. Which verification applies is decided by where the review is, not by what the caller claims. A review at MERGE is the merge queue's: this is for the environment the queue promoted once it has fetched the review's target and source, built the prospective squash merge, recorded that build (review_record-build), and pushed the result. buildId must name that successful recorded build of the merge commit, and the platform fetches remoteUrl to confirm the build's commit is really reachable from the target branch's tip and descends from, without being, the target tip this review was queued against. Any other review is one whose work landed without the queue — in practice a GitHub squash merge, where no merge-commit build exists to name: omit buildId, and the platform confirms against the same remote that everything the review's source branch adds is already present in the target branch's history. Either way a check failing refuses with 409 MERGE_NOT_VERIFIED — a branch that did not land is refused just as firmly. A real, immediate write, not a preview, unless preview is set.",
	}, review.ReviewReportMergedTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "review_requeue",
		Description: "Move a review stuck at MERGE back to READY on the erun platform, freeing its target branch's merge-queue slot so a different review can be promoted — only one review may be at MERGE per target branch at a time. For a review whose queued build never finishes, or one left at MERGE by a batched exec_gate-merge whose other members landed but were never promoted. The review rejoins the queue at the tail, not the head. Refuses, naming the review's actual status, when it is not at MERGE. A real, immediate write, not a preview, unless preview is set.",
	}, review.ReviewRequeueTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "review_reviewers_list",
		Description: "List the users assigned to review a review on the erun platform. Supports preview.",
	}, review.ReviewReviewersListTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "review_reviewers_add",
		Description: "Assign a reviewer to a review on the erun platform, so an Agent can assign a peer Agent (or itself) as reviewer. userId must already be enrolled in the caller's own tenant — refused before the network call otherwise. Assigning a reviewer gates no status transition; use review_queue_advance's unresolved-thread gate for what actually blocks a merge. A real, immediate write, not a preview, unless preview is set.",
	}, review.ReviewReviewerAddTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "review_reviewers_remove",
		Description: "Remove a reviewer from a review on the erun platform. A real, immediate write, not a preview, unless preview is set.",
	}, review.ReviewReviewerRemoveTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "review_queue_list",
		Description: "List a target branch's merge queue on the erun platform, in queue order. Supports preview.",
	}, review.ReviewMergeQueueListTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "jobs_list",
		Description: "List jobs on the erun platform, the live queue first — what agents and orchestrators are working on right now, and what recently finished. Each entry names what is being done, by whom, and how long it has been going. A RUNNING job is work in flight, and so is PLANNED — work recorded before it starts, which the abandonment sweep deliberately leaves alone; ABANDONED means its actor stopped updating it and the platform swept it — read it as dropped, not as failed. Supports preview.",
	}, jobs.JobsListTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "jobs_show",
		Description: "Show one job in full on the erun platform. Supports preview.",
	}, jobs.JobsShowTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "jobs_start",
		Description: "Record that this actor is starting a piece of work, so the queue shows what is in flight before it finishes and a second actor can see it. With scope set this is a claim: a 409 means another open job already holds that scope, and the refusal names who holds it, what they are doing in prose, and since when — pick up something else rather than duplicating the work. A PLANNED job holds its scope too. status PLANNED records work that has not started — a triage or plan item parked against its issue, visible in the pipeline view before any code exists, and deliberately never swept to ABANDONED. The summary is prose describing the work, never the command that performs it. A real, immediate write, not a preview, unless preview is set.",
	}, jobs.JobsStartTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "jobs_finish",
		Description: "Report how a job ended, refresh what it is doing, or open a planned job, on the erun platform. status RUNNING on a PLANNED job is the one move that begins work; every other status closes the job. A job that has already finished cannot be updated: its outcome is the record coordination and reporting both read. A real, immediate write, not a preview, unless preview is set.",
	}, jobs.JobsFinishTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "review_queue_advance",
		Description: "Advance a target branch's merge queue head to MERGE on the erun platform, which starts that review's merge-queue build. Refuses with the unresolved comment thread count when the queue head still has open threads — resolve them (review_resolve) or use review_queue_override_advance. A real, immediate mutation of shared control-plane state, not a preview, unless preview is set.",
	}, review.ReviewMergeQueueAdvanceTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "review_queue_override-advance",
		Description: "Bypass review_queue_advance's unresolved-thread gate and advance a target branch's merge queue head to MERGE anyway. reason is required and is recorded in the platform's audit trail alongside the caller's identity — this is a deliberate, accountable escape hatch, not a routine way to advance the queue. A real, immediate mutation of shared control-plane state, not a preview, unless preview is set.",
	}, review.ReviewMergeQueueOverrideAdvanceTool(runtime))
}

func registerDeliveryTools(reg shared.ToolRegistrar, runtime shared.RuntimeConfig) {
	shared.AddTool(reg, &mcp.Tool{
		Name:        "init",
		Description: "Run `erun init` using the shared init flow; when more input is needed, return a structured interaction request for the caller to answer in a follow-up tool call",
	}, initenv.InitTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "build",
		Description: "Build the project's container images for the resolved tenant/environment. The build step of the build → release → push → deploy flow: set deploy to push and roll them out, or release to stamp and publish the release version first.",
	}, build.BuildTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "push",
		Description: "Build and push the project's container images to the BUILD-marked registry for the resolved tenant/environment. The push step of the build → release → push → deploy flow.",
	}, build.PushTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "deploy",
		Description: "Roll the project's charts out to the resolved tenant/environment: build and push the images they need, mirror them from the FROM to the TO registry when both roles are marked, then run the rollout with the cluster pulling from the DEPLOY registry. The deploy step of the build → release → push → deploy flow. Waits for the rollout to become ready (default 5m, the env's deploy.timeout, or the timeout input) and watches the new pods, keeping the wait while an image is still pulling and aborting early on a real container failure.",
	}, deploy.DeployTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "publish",
		Description: "Mirror an already-built version's images from the FROM registry to each TO registry for the resolved tenant/environment, without building or deploying. Use it to hand a version you have tested (e.g. built against a local cluster registry) to other users by copying that exact multi-arch image to the shared TO registry. Requires a version (produced by build then push) and a FROM source plus at least one TO destination marked in the registry list.",
	}, build.PublishTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "upgrade",
		Description: "Redeploy the resolved environment to the latest version for its release channel when it is opted into Upgrade all (autoupgrade) and lags. Snapshot-channel environments adopt a stable release once one is published on top of the latest snapshot. High blast radius: rolls out a new runtime image and restarts pods. Set preview to resolve and return the plan (channel, current → target) without deploying.",
	}, upgrade.UpgradeTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "doctor",
		Description: "Diagnose and repair the resolved environment: report why a deploy may have failed (helm release status and runtime pods, read-only); recover a failing runtime release by clearing a stuck pending helm lock or rolling back to the last successful revision; prune unused Docker images, build cache, or stopped containers; and optionally restore or repair the root erun config from a backup or by re-initializing orphaned cloud provider aliases",
	}, doctor.DoctorTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "observe",
		Description: "Report the resolved environment's Kubernetes state, read-only: pods (each container's name, running image, and resource limits), ResourceQuota/LimitRange usage, Ingress hosts and TLS secret names, Certificate readiness, and the runtime helm release (chart/app version, revision, status, and the image overrides and runtime resource limits erun itself set). When a Certificate is not Ready, its CertificateRequest -> Order -> Challenge chain is walked automatically for the failure reason, so a stuck issuance (for example a webhook solver's RBAC denial) surfaces in this one call. The release is diffed against the running containers and against the env config's recorded runtimeversion/runtimeimage/runtimepod, so a drifted deploy (an out-of-band image, a resized pod the release never recorded) is named in the result's `drift` list rather than left for the caller to spot, and that list is empty when the comparison found nothing to report. Optionally check named Secrets for a key's presence without reading their values. Every call is a kubectl get or a helm status; nothing here can mutate the cluster.",
	}, observe.ObserveTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name: "usage",
		Description: "Report the resolved environment's live CPU, memory, and disk usage, read straight from the runtime container's cgroup v2 accounting and a statfs of the workspace mount -- no metrics-server required, so it works on clusters where `kubectl top` reports \"Metrics API not available\". Memory is reported against the container's own limit (current, peak high-water mark, and a real OOM-kill count) and CPU utilisation against its quota over a sample window; a named warning fires when memory, memory's peak, or disk usage cross a fixed threshold. Every field reports its own unavailability (cgroup v1, an unlimited limit, an unreadable file) rather than failing the call. `disk[].totalBytes`/`usedBytes`/`percentUsed` describe the whole mount (`nodeShared: true`): every environment scheduled on the same node reports the identical figures, so cleaning up one environment may barely move them. `disk[].ownUsedBytes` (a `du` of the mount, scoped to this environment's own directory) is the figure this environment can actually reduce. `requests` reports what the scheduler admits this pod on: each container's declared `resources.requests` and the pod's effective total, read from the live pod spec because no cgroup file records a request. The limits above it are ceilings that reserve nothing, and the request is the figure that decides placement and how much of a node this environment actually holds; a pod spec that could not be read is reported as `requests.unavailable` rather than as a reservation of nothing. On a build-capable environment (local-agent, remote-agent), `excludesBuilds` is true because every image build actually runs in the erun-dind sidecar, a separate cgroup from the runtime container above -- so this call also reads the sidecar's own CPU and memory against its own limit and returns it as `dind`, with the same named warnings if the sidecar itself nears its memory limit or records an OOM kill; `dind` is absent only when the sidecar's own cgroup could not be read, and `observe` reports its resource limits either way. Also carries this environment's standing sizing recommendation (`sizing`), when retained usage history has accumulated one -- the same raise/lower/hold verdict and evidence window `resize` reasons from, so a caller does not need a separate resize call just to see it. That history is retained by this environment's own pod monitor and lives with the environment, so " +
			usage.UsageSizingClaim() + ". What varies is whether that history is there to read -- a reading taken from a host has the live counters to reason from and no observed window: the advice the counters prove (a raise, after a peak at the limit or a recorded OOM kill) fires there too, while the shrink direction, which needs a day of quiet evidence, reads as insufficient-evidence. ",
	}, usage.UsageTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "resize",
		Description: "Change the runtime pod's CPU/memory limits and roll it out, without re-running init to change two numbers. Pass cpu/memory explicitly, or applyRecommendation to size from this environment's own standing sizing recommendation (resolved from usage history retained in this pod) instead of retyping a value already computed. A resize whose resolved size matches the current one is a no-op. Moves only the runtime container's throttle/OOM ceiling and its namespace quota draw -- never the scheduler's fixed request, the erun-dind sidecar, or any PVC. Because a resize rolls the pod (Recreate strategy) and would kill any live session inside it, it first checks this environment's activity leases and refuses, naming every holder, unless overrideLease is set. Supports preview.",
	}, resize.ResizeTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "delete",
		Description: "Delete an environment from ERun configuration and remove its remote runtime namespace after explicit tenant-environment confirmation",
	}, envdelete.DeleteTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "services",
		Description: "Report the resolved environment's Services, read-only: each Service's name, type, ports, and the public address it already has when an erun-expose Ingress fronts it. The exposure is read from the Ingress's own backend rather than re-derived from the <tenant>-<service> naming convention, so a repo-native chart that names its Service something else still reports the Service its Ingress really routes to. This is the read that answers \"what can I expose here, and under what name\" before `expose`, which otherwise takes a name nothing on the wire can supply. Both reads are plain kubectl gets; nothing here can mutate the cluster. Supports preview.",
	}, services.ServicesTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "expose",
		Description: "Expose an in-namespace Service at a stable public hostname under the platform's services zone (requires a platform block in .erun/config.yaml): ensure the per-environment wildcard DNS record points at the env's ingress IP and apply a Host-routing Ingress. The Ingress routes to <tenant>-<service> unless backendService names the in-namespace Service explicitly, which any repo with its own chart needs -- a derived name no Service holds yields a hostname that resolves and an ingress that 503s; the `services` tool lists the candidates. The Ingress references a per-env wildcard TLS Secret by default; nothing populates it unless dns01TokenFile, dns01BrokerUrl, and acmeEmail are also set, in which case it also provisions a namespaced cert-manager Issuer + Certificate through erun's DNS-01 broker. Supports preview.",
	}, expose.ExposeTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "e2e",
		Description: "Discover this project's e2e suites -- <tenant>-devops/e2e/<component>/Dockerfile, each declaring an `e2e` stage -- the way build discovers docker/ and deploy discovers k8s/, then run each one as `docker build --target e2e --no-cache-filter e2e --output type=cacheonly` against this server's own resolved environment, every suite in order unless component names one. Each suite receives ERUN_E2E_VERSION (the deployed version), ERUN_E2E_BASE_URL (the HTTPS URL of the exposed service named like the suite's directory) and ERUN_E2E_URL_<SERVICE> for every exposed service as build args, plus the .erun/config.yaml e2e.secrets as --secret. Refuses before any suite runs, naming the cause, when the environment is not deployed, a suite's service is not exposed or its certificate is not yet issued, or its URL resolves to a loopback address a docker build cannot reach; also refuses a suite whose playwright.config sets ignoreHTTPSErrors or hardcodes baseURL. baseUrl replaces the exposed URL and skips the exposure and certificate checks; version replaces the deployed version the suites expect, and with both the environment is not read at all. A failing suite fails the call with its component, target URL, deployed version and log path; e2e is not a release or merge gate and files nothing itself. A project with no e2e/ suite is a clean no-op. Supports preview.",
	}, e2e.E2eTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "unexpose",
		Description: "Remove an environment's per-env wildcard DNS record — the DNS-side counterpart to expose, run at environment teardown so records don't accumulate for environments that no longer exist. Touches only the platform DNS zone; the Ingress that referenced the record lives in the environment's own namespace and is torn down with it. Requires a platform block in .erun/config.yaml unless servicesZone and platformNamespace are both set. Supports preview.",
	}, expose.UnexposeTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name: "pin",
		Description: "Re-pin every erun version reference for tenant/environment (defaulting to the MCP runtime context) in one motion: the Terraform module ?ref, an erun image reference set directly in Terraform variables (e.g. the cluster-edge module's dns01_webhook_image), each umbrella chart's erun chart dependencies, the build-env image tag, a stated runtime chart or runtime image naming erun's own stock release, and the environment's own runtime version when that environment runs erun's own runtime image. " +
			"Rewrites projectRoot (or, when unset, the runtime repo path); refuses rather than scan a wider directory when neither resolves, since a caller here has no working directory to fall back to the way a shell user does. " +
			"They only work when they agree, and nothing else keeps them in step. Idempotent and a no-op once aligned. Refuses a version that is not published. Set revert to go back to the version recorded before the last re-pin. " +
			"Set preview to return the full plan — every site, old and new — without writing. Edits the source of truth only: realizing the version (terraform apply, deploy) stays a separate explicit step.",
	}, pin.PinTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "terraform",
		Description: "Run a hosted platform's per-environment Terraform from terraform-<tenant>/<environment>/ (or <tenant>-devops/terraform-<tenant>/<environment>/, or the paths.terraform base from .erun/config.yaml) for the resolved tenant/environment: resolve the env folder, pick up the symlinked common.tf, and run its main.tf with <environment>.tfvars. State and the provider cache live on the durable home directory (not the playbook tree), so they survive a runtime pod restart. operation is apply (init → fmt → plan → apply), plan (read-only), or destroy. apply/destroy mutate real cloud and cluster state and require confirm to equal the environment name. Injects TF_VAR_cloudflare_api_token from CLOUDFLARE_API_TOKEN when present. Set preview to resolve and return the terraform commands without executing them.",
	}, terraform.TerraformTool(runtime))
}

func registerInspectionTools(reg shared.ToolRegistrar, runtime shared.RuntimeConfig) {
	shared.AddTool(reg, &mcp.Tool{
		Name:        "exec_diff",
		Description: "Return the current git diff from the runtime repo root as raw text plus structured file, hunk, line, and tree data",
	}, diff.DiffTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "exec_raw",
		Description: "Run an arbitrary command from the runtime repo root and return captured stdout, stderr, and trace output",
	}, exectool.RawTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "exec_write",
		Description: "Write content to a path in the runtime repo's working tree, taking the content as data — never through a shell, so nothing in it is reinterpreted. Refuses if the resolved path would land outside the repo root. Reports the resolved path and byte count written. Set preview to trace the write without performing it.",
	}, exectool.WriteTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "exec_commit",
		Description: "Stage every change (or, with paths set, only those paths) in the runtime repo's working tree and commit it with a message taken as data — never through a shell. branch must match the tree's actual current branch; the commit is refused, loudly, when it does not, rather than landing on whichever branch HEAD happens to be on. When paths is set, the commit is refused just as loudly if the tree has changes outside the declared paths, so an unrelated writer's edits can never be absorbed into it. Reports the branch, commit id, and files committed. Set preview to verify the branch and trace the files that would be committed without committing.",
	}, exectool.CommitTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "exec_push",
		Description: "Push the runtime repo's working tree's current branch to a remote. branch must match the tree's actual current branch; the push is refused, loudly, when it does not, rather than pushing whichever branch HEAD happens to be on. A real, immediate mutation of shared remote state — a branch a hosted review or another reviewer can only ever fetch once it has actually landed there. Set preview to verify the branch and trace the push without running it.",
	}, exectool.ExecPushTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "exec_merge",
		Description: "Fetch targetBranch from a remote and merge it into the runtime repo's working tree's current branch with an explicit merge commit — never a rebase, since review comments anchor to a commit id and a rewrite would orphan every thread on an open review. A conflicted merge is reported as a distinct, named outcome; the worktree is left exactly as git left it, mid-merge, for the caller to resolve or abort — unless the conflict is confined to atlas.sum, the regenerable migration checksum, which is regenerated from the migration files already on disk and staged, completing the merge commit so the merge lands instead. A real, immediate mutation of the working tree. Set preview to trace the fetch and merge without running them.",
	}, exectool.ExecMergeTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "exec_gate-merge",
		Description: "Prepare the prospective merge a merge queue promotion (or batch) builds: fetch targetBranch and every source's branch, check out a fresh local branch named targetBranch at its own current remote tip, then squash-merge each source onto it in turn, each as its own commit carrying its own message plus that source branch's own load-bearing trailers (Closes #N, Defect-Fix:, and the Reproduces: / Regression-Test: lines a defect fix declares), read from every line of its commit bodies rather than from a trailing trailer block so a declaration is not lost to whatever paragraph an author put after it — leaving a stack of one commit per landed source, with each landed source's carriedTrailers naming what it carried, including when it carried none. Passing more than one source batches several unmerged branches into one prospective merge, so the build that follows tests whether they compile *together*, not just individually; a single source is the ordinary one-branch merge. For the environment a review's merge queue promotes to MERGE: gate-merge, then a plain build of the result, then review_record-build with the version that build minted and, only on success, exec_push and review_report-merged. The working tree must already be clean — this checks out a different local branch than whatever the tree is currently on, so uncommitted work there is refused rather than silently carried onto the prospective merge. A source whose squash conflicts is skipped, not fatal: the merge is backed out and recorded in the result's skipped list, and the rest of the batch still builds against the working tree as it stood before that attempt. A conflict confined to atlas.sum, the regenerable migration checksum, is not a skip: it is regenerated from the migration files already on disk and staged, and the source lands like a clean squash. A source that contributes nothing is skipped the same way: when its squash stages no changes because its content is already on the target, it is recorded as skipped with that reason rather than failing the batch, so an already-landed branch is a no-op instead of a dead merge. A defect fix whose declaration does not resolve against what its squash staged is skipped the same way, with a regression-test policy: reason. A batch where every source is skipped lands nothing and returns an error. A real, immediate mutation of the working tree. Set preview to trace the fetch, checkout, and each squash merge and commit without running them.",
	}, exectool.ExecGateMergeTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "exec_report-commit-status",
		Description: "Report a commit status on GitHub for commit — the last step in the merge queue (exec_gate-merge, build, review_record-build): report success once the recorded build succeeded, or failure the moment it did not, naming which step failed in description. A required status check on the remote's branch protection has nothing to require until this reports it. commit should be the review's source branch tip — the pull request's own head commit — never the local prospective squash-merge commit exec_gate-merge produces: GitHub only evaluates a required check against a commit reachable from the open pull request, and the squash commit does not exist there until after the build has already succeeded and been pushed. Set preview to trace the request without sending it.",
	}, exectool.ExecReportCommitStatusTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "exec_close-pr",
		Description: "Close branch's open pull request on GitHub, once review_report-merged has already succeeded, and record landingCommit on it. GitHub never reconciles a queued merge with its pull request on its own, since exec_gate-merge's squash commit is never the branch head GitHub tracks — the commit that actually shipped exists nowhere the pull request can see until this reports it. Safe when branch has no open pull request against targetBranch: this is a no-op, not an error, since queueing a plain branch with no review is legitimate. Refuses, loudly, when the pull request's current head does not match gatedCommit — something pushed to branch after the merge queue fetched it, so the built content is not what closing would discard. Set preview to trace the lookup without closing or commenting on anything.",
	}, exectool.ExecClosePRTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "exec_reconcile-bypass",
		Description: "Cross-reference GitHub's own bypass ledger for rulesetId on targetBranch against erun's recorded builds: every push that bypassed rulesetId is reported next to whether a successful build was recorded for a commit it actually landed (RECONCILED, naming that buildId), a release tag accounts for it (RELEASE), or nothing does (UNRECONCILED). The reconciliation half of erun#1912's recorded decision -- narrowing who may hold the bypass grant is a separate, ops-side change; this makes every bypass accountable after the fact regardless of who holds it. The result's unreconciled count and each push's verdict are the loud signal: a caller must not treat a non-zero unreconciled count as routine. Set preview to trace the GitHub and platform lookups without sending them.",
	}, exectool.ExecReconcileBypassTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "exec_plan-ruleset-bypass",
		Description: "Plan narrowing a protected branch's ruleset bypass grant to one non-human queue identity. A merge queue's push is itself a direct push, so it must bypass the branch's pull-request rule; that is only accountable if exactly one nameable, non-human identity can bypass at all — and GitHub's bypass is per-actor, not per-rule, so adding a required status check on top of a broad always grant changes nothing for whoever holds it. Resolves the exact edit from the ruleset as it actually is, in two stages that never leave the branch unmergeable: stage 1 grants queueActor an always bypass alongside today's actors, stage 2 demotes every other always actor to pull_request. Refuses when the queue identity cannot already push, when GitHub will not show the ruleset's bypass actors, or when targetBranch is not a branch this ruleset governs. Writes stage1, stage2 and rollback payload files and returns the paths; it never writes to GitHub, so applying the edit stays a deliberate human step. Set preview to trace the lookups and the files it would write without sending or writing anything.",
	}, exectool.ExecPlanRulesetBypassTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "exec_route-check",
		Description: "Prove every route erun-backend-api's router registers is actually reachable on a deployed plane, rather than trusting that merged code is deployed code. Reads the route inventory straight out of erun-backend-api's own source (never a hand-maintained list) and GETs each one; first sanity-probes GET /v1/whoami and reports the plane unreachable rather than every route missing when that does not answer. Every probe is a plain GET regardless of a route's own registered method — the router reports 405 for a path it knows under a different method — so this never risks creating, updating, or deleting anything, and only the plane's own unmodified 404 page means a route was never registered at all. The result's planeReachable, missing, and errors fields are the loud signal: a caller must not treat a non-empty missing list as routine. Set preview to trace the resolved plane and route inventory without sending any request.",
	}, expose.ExecRouteCheckTool(runtime))

	// Deprecated aliases for the four exec tools, kept callable for one release.
	// `erun exec` was the only command group on the surface whose tools
	// dropped their prefix, and it is the group whose members differ most in
	// blast radius -- exec_diff only reads, exec_raw runs arbitrary argv -- so
	// the rename is worth the alias window rather than a hard break. Each alias
	// shares its replacement's handler and resolves to the same descriptor and
	// capability, so nothing about authorization or auditing changes with the
	// name a caller happens to use.
	shared.AddTool(reg, &mcp.Tool{
		Name:        "diff",
		Description: "Deprecated: use exec_diff. Retained for one release; this name will be removed.",
	}, diff.DiffTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "raw",
		Description: "Deprecated: use exec_raw. Retained for one release; this name will be removed.",
	}, exectool.RawTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "write",
		Description: "Deprecated: use exec_write. Retained for one release; this name will be removed.",
	}, exectool.WriteTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "commit",
		Description: "Deprecated: use exec_commit. Retained for one release; this name will be removed.",
	}, exectool.CommitTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "outputs_list",
		Description: "List the files and folders an agent produced in the runtime pod's outputs directory ($ERUN_OUTPUTS_DIR, default /home/erun/.erun/outputs), newest-first. Read-only.",
	}, outputs.OutputsListTool())
	shared.AddTool(reg, &mcp.Tool{
		Name:        "outputs_download",
		Description: "Read one entry from the runtime pod's outputs directory and return its bytes inline as base64 (a folder as a tar.gz archive). The server runs in the pod, so it returns the content directly for the caller to save. On a macOS host an unsigned macOS binary is ad-hoc signed first, because the system kills an unsigned one on exec without printing anything; the signing field reports it. Set preview to return name/type/size without the bytes.",
	}, outputs.OutputsDownloadTool())
	shared.AddTool(reg, &mcp.Tool{
		Name:        "build_profile",
		Description: "List recent erun build runs (newest-first), or return one build's full step tree -- duration, CPU seconds against the build's cgroup quota, throttled periods, and I/O per step -- when id is set. Reads the same ~/.erun/timing/build-*.json records `erun build` already writes. Read-only.",
	}, build.BuildProfileTool())
	shared.AddTool(reg, &mcp.Tool{
		Name:        "release",
		Description: "Cut a project release from the runtime repo root using .erun/config.yaml branch policy. Stamps the release version into the charts and packaging metadata, commits and tags it locally, then pushes the tag, syncs packaging checksums, prepares the next patch version, and pushes the branches. It marks source control and nothing else: it never builds, publishes, or verifies an artifact, so it completes having published nothing and says so. Build and publish belong to `erun build --release`, which composes this same stamp/tag work with the build and the publish, and to `erun push --version <version>`. Set preview to resolve and return the plan without executing it.",
	}, build.ReleaseTool(runtime))
	shared.AddTool(reg, &mcp.Tool{
		Name:        "contribute_clone",
		Description: "Clone the ERun source repository into $HOME/git/erun inside the environment so contribute-mode tabs can build and run a local ERun checkout",
	}, contribute.ContributeCloneTool(runtime))
}
