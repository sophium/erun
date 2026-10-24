package mcptools

import (
	"sort"
	"strings"
)

// Tool families and blast-radius hints are wire contract. The SDK treats nil destructive and
// open-world hints as true, so every tool must be described explicitly.

// MCPToolDescriptor is the wire-visible metadata for one tool.
type MCPToolDescriptor struct {
	// Family is exposed as _meta.family; tool names cannot be split on "_" reliably.
	Family string
	// CLIPath is the erun command behind the tool; nil for wire-only tools.
	CLIPath []string
	Title   string
	// ReadOnly is semantic, not derived from mcpReadOnlyTools, which is a stricter authorization allowlist.
	ReadOnly bool
	// Destructive is meaningful only when ReadOnly is false.
	Destructive bool
	Idempotent  bool
	// OpenWorld means the tool reaches outside the environment: a registry, cloud API, control plane, or arbitrary argv.
	OpenWorld bool
	// AgentFacing exempts an automation-only tool from the desktop-surface gate; false means
	// erun-ui/frontend must offer a way in.
	AgentFacing bool
}

// mcpToolDescriptors must cover every registered tool; the transport refuses to register one missing here.
var mcpToolDescriptors = map[string]MCPToolDescriptor{
	// The exec_* family is an agent's hands in its own worktree, so it is agent-facing.
	"exec_diff":       {Family: "exec", CLIPath: []string{"exec", "diff"}, Title: "Show repository diff", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false, AgentFacing: true},
	"exec_raw":        {Family: "exec", CLIPath: []string{"exec", "raw"}, Title: "Run a raw command in the environment", ReadOnly: false, Destructive: true, Idempotent: false, OpenWorld: true, AgentFacing: true},
	"exec_write":      {Family: "exec", CLIPath: []string{"exec", "write"}, Title: "Write a file in the working tree", ReadOnly: false, Destructive: true, Idempotent: false, OpenWorld: false, AgentFacing: true},
	"exec_commit":     {Family: "exec", CLIPath: []string{"exec", "commit"}, Title: "Commit working-tree changes", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: false, AgentFacing: true},
	"exec_push":       {Family: "exec", CLIPath: []string{"exec", "push"}, Title: "Push a working-tree branch to a remote", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true, AgentFacing: true},
	"exec_merge":      {Family: "exec", CLIPath: []string{"exec", "merge"}, Title: "Merge a branch into the working tree", ReadOnly: false, Destructive: true, Idempotent: false, OpenWorld: false, AgentFacing: true},
	"exec_gate-merge": {Family: "exec", CLIPath: []string{"exec", "gate-merge"}, Title: "Compose the prospective merge commit a merge queue promotion (or batch) builds", ReadOnly: false, Destructive: true, Idempotent: false, OpenWorld: false, AgentFacing: true},
	"exec_report-commit-status": {
		Family: "exec", CLIPath: []string{"exec", "report-commit-status"}, Title: "Report a GitHub commit status for a merge queue build result",
		ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true, AgentFacing: true,
	},
	"exec_close-pr": {
		Family: "exec", CLIPath: []string{"exec", "close-pr"}, Title: "Close the GitHub pull request a merge queue actually shipped",
		ReadOnly: false, Destructive: true, Idempotent: true, OpenWorld: true, AgentFacing: true,
	},
	"exec_reconcile-bypass": {
		Family: "exec", CLIPath: []string{"exec", "reconcile-bypass"}, Title: "Check ruleset-bypassed pushes against successful builds",
		ReadOnly: true, Destructive: false, Idempotent: true, OpenWorld: true, AgentFacing: true,
	},
	"exec_plan-ruleset-bypass": {
		Family: "exec", CLIPath: []string{"exec", "plan-ruleset-bypass"}, Title: "Plan narrowing a ruleset's bypass grant to one queue identity",
		ReadOnly: true, Destructive: false, Idempotent: true, OpenWorld: true, AgentFacing: true,
	},
	"exec_route-check": {
		Family: "exec", CLIPath: []string{"exec", "route-check"}, Title: "Prove every registered API route is reachable on a deployed plane",
		ReadOnly: true, Destructive: false, Idempotent: true, OpenWorld: true, AgentFacing: true,
	},
	// exec_agent has no CLI path: `erun exec job start --agent` covers it, but MCP needs a fixed schema per tool.
	"exec_agent":            {Family: "exec", CLIPath: nil, Title: "Run an AI tool as a detached job", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true, AgentFacing: true},
	"exec_job_attach":       {Family: "exec", CLIPath: []string{"exec", "job", "attach"}, Title: "Attach to a running job", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: false, AgentFacing: true},
	"exec_job_status":       {Family: "exec", CLIPath: []string{"exec", "job", "status"}, Title: "Report a job's state and outcome", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false, AgentFacing: true},
	"exec_job_await":        {Family: "exec", CLIPath: []string{"exec", "job", "await"}, Title: "Wait for a job to reach a terminal state", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false, AgentFacing: true},
	"exec_job_output":       {Family: "exec", CLIPath: []string{"exec", "job", "output"}, Title: "Read a job's captured output", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false, AgentFacing: true},
	"exec_job_cancel":       {Family: "exec", CLIPath: []string{"exec", "job", "cancel"}, Title: "Cancel a running job", ReadOnly: false, Destructive: true, Idempotent: true, OpenWorld: false, AgentFacing: true},
	"cloud_list":            {Family: "cloud", CLIPath: nil, Title: "List configured cloud provider aliases", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	"cloud_init_aws":        {Family: "cloud", CLIPath: []string{"cloud", "init", "aws"}, Title: "Register an AWS provider alias", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"cloud_init_cloudflare": {Family: "cloud", CLIPath: []string{"cloud", "init", "cloudflare"}, Title: "Register a Cloudflare provider alias", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"cloud_init_erun":       {Family: "cloud", CLIPath: []string{"cloud", "init", "erun"}, Title: "Register an erun platform alias", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"cloud_login":           {Family: "cloud", CLIPath: []string{"cloud", "login"}, Title: "Sign in to a cloud provider", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"cloud_oidc":            {Family: "cloud", CLIPath: []string{"cloud", "oidc"}, Title: "Configure OIDC for a provider alias", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"cloud_set":             {Family: "cloud", CLIPath: []string{"cloud", "set"}, Title: "Set an environment's cloud alias", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: false},
	// Credential injection is plumbing behind cloud_login.
	"cloud_inject_aws_credentials": {Family: "cloud", CLIPath: nil, Title: "Inject AWS credentials into an environment", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: false, AgentFacing: true},
	"cloud_clear_aws_credentials":  {Family: "cloud", CLIPath: nil, Title: "Clear injected AWS credentials", ReadOnly: false, Destructive: true, Idempotent: true, OpenWorld: false, AgentFacing: true},
	"context_list":                 {Family: "context", CLIPath: []string{"context", "list"}, Title: "List cloud contexts", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	"context_init":                 {Family: "context", CLIPath: []string{"context", "init"}, Title: "Bootstrap a new cloud context", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"context_start":                {Family: "context", CLIPath: []string{"context", "start"}, Title: "Start a cloud context", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"context_stop":                 {Family: "context", CLIPath: []string{"context", "stop"}, Title: "Stop a cloud context", ReadOnly: false, Destructive: true, Idempotent: true, OpenWorld: true},
	"platform_whoami":              {Family: "platform", CLIPath: []string{"platform", "whoami"}, Title: "Report the authenticated platform identity", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_version":             {Family: "platform", CLIPath: []string{"platform", "version"}, Title: "Report the erun platform's own version and build", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_tenant_list":         {Family: "platform", CLIPath: []string{"platform", "tenant", "list"}, Title: "List platform tenants", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_tenant_create":       {Family: "platform", CLIPath: []string{"platform", "tenant", "create"}, Title: "Create a platform tenant", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	// A break-glass repair run directly against the broken platform, with no ongoing UI affordance.
	"platform_tenant_repair-org-mapping": {Family: "platform", CLIPath: []string{"platform", "tenant", "repair-org-mapping"}, Title: "Repair a tenant's dead org mapping", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true, AgentFacing: true},
	"platform_identity_org_create":       {Family: "platform", CLIPath: []string{"platform", "identity", "org", "create"}, Title: "Create an organization on the platform's identity provider", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_user_list":                 {Family: "platform", CLIPath: []string{"platform", "user", "list"}, Title: "List platform users", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_user_enroll":               {Family: "platform", CLIPath: []string{"platform", "user", "enroll"}, Title: "Enrol a user into a tenant", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_user_grant-role":           {Family: "platform", CLIPath: []string{"platform", "user", "grant-role"}, Title: "Grant a role to an enrolled user", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"platform_env_list":                  {Family: "platform", CLIPath: []string{"platform", "env", "list"}, Title: "List hosted environments", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_env_get":                   {Family: "platform", CLIPath: []string{"platform", "env", "get"}, Title: "Get one hosted environment", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_env_register":              {Family: "platform", CLIPath: []string{"platform", "env", "register"}, Title: "Register a hosted environment", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_env_deploy":                {Family: "platform", CLIPath: []string{"platform", "env", "deploy"}, Title: "Deploy a hosted environment", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_env_stop":                  {Family: "platform", CLIPath: []string{"platform", "env", "stop"}, Title: "Stop a hosted environment", ReadOnly: false, Destructive: true, Idempotent: true, OpenWorld: true},
	"platform_env_delete":                {Family: "platform", CLIPath: []string{"platform", "env", "delete"}, Title: "Delete a hosted environment and its namespace", ReadOnly: false, Destructive: true, Idempotent: true, OpenWorld: true},
	"platform_context_list":              {Family: "platform", CLIPath: []string{"platform", "context", "list"}, Title: "List platform cloud contexts", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_context_get":               {Family: "platform", CLIPath: []string{"platform", "context", "get"}, Title: "Get one platform cloud context", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_context_create":            {Family: "platform", CLIPath: []string{"platform", "context", "create"}, Title: "Create a platform cloud context", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_provision":                 {Family: "platform", CLIPath: []string{"platform", "provision"}, Title: "Preview the plan for provisioning a hosted environment", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"review_list":                        {Family: "review", CLIPath: []string{"review", "list"}, Title: "List reviews", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"review_show":                        {Family: "review", CLIPath: []string{"review", "show"}, Title: "Show a review, its comments, and its builds", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"review_create":                      {Family: "review", CLIPath: []string{"review", "create"}, Title: "Open a review", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"review_comment":                     {Family: "review", CLIPath: []string{"review", "comment"}, Title: "Comment on a review", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"review_close":                       {Family: "review", CLIPath: []string{"review", "close"}, Title: "Close a review", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"review_record-build":                {Family: "review", CLIPath: []string{"review", "record-build"}, Title: "Record a build against a review", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true, AgentFacing: true},
	"review_report-merged":               {Family: "review", CLIPath: []string{"review", "report-merged"}, Title: "Report a review MERGED, verified as a queue-driven merge or as work that landed elsewhere", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true, AgentFacing: true},
	"review_requeue":                     {Family: "review", CLIPath: []string{"review", "requeue"}, Title: "Move a review stuck at MERGE back to READY", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true, AgentFacing: true},
	"review_reviewers_list":              {Family: "review", CLIPath: []string{"review", "reviewers", "list"}, Title: "List a review's assigned reviewers", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"review_reviewers_add":               {Family: "review", CLIPath: []string{"review", "reviewers", "add"}, Title: "Assign a reviewer to a review", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"review_reviewers_remove":            {Family: "review", CLIPath: []string{"review", "reviewers", "remove"}, Title: "Remove a reviewer from a review", ReadOnly: false, Destructive: true, Idempotent: true, OpenWorld: true},
	"review_resolve":                     {Family: "review", CLIPath: []string{"review", "resolve"}, Title: "Resolve a comment thread", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"review_unresolve":                   {Family: "review", CLIPath: []string{"review", "unresolve"}, Title: "Reopen a comment thread", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"review_queue_list":                  {Family: "review", CLIPath: []string{"review", "queue", "list"}, Title: "List a merge queue", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"review_queue_advance":               {Family: "review", CLIPath: []string{"review", "queue", "advance"}, Title: "Advance a merge queue", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"review_queue_override-advance":      {Family: "review", CLIPath: []string{"review", "queue", "override-advance"}, Title: "Override the unresolved-thread gate and advance a merge queue", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	// jobs_list/jobs_show have an erun-console surface; jobs_start/jobs_finish are self-reports.
	"jobs_list":   {Family: "jobs", CLIPath: []string{"jobs", "list"}, Title: "List jobs: what is being worked on now, and what recently finished", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"jobs_show":   {Family: "jobs", CLIPath: []string{"jobs", "show"}, Title: "Show one job in full", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"jobs_start":  {Family: "jobs", CLIPath: []string{"jobs", "start"}, Title: "Record that this actor is starting a piece of work", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true, AgentFacing: true},
	"jobs_finish": {Family: "jobs", CLIPath: []string{"jobs", "finish"}, Title: "Report a job's progress or its outcome", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true, AgentFacing: true},
	"idle":        {Family: "idle", CLIPath: []string{"idle"}, Title: "Report an environment's idle and auto-stop state", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	// Humans read auto-stop history through the `idle` report.
	"idle_stop_history": {Family: "idle", CLIPath: nil, Title: "List recorded auto-stop decisions", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false, AgentFacing: true},
	"idle_stop_record":  {Family: "idle", CLIPath: nil, Title: "Record an auto-stop decision", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: false, AgentFacing: true},
	"idle_stop_cancel":  {Family: "idle", CLIPath: nil, Title: "Cancel a pending auto-stop", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: false, AgentFacing: true},
	"whip":              {Family: "whip", CLIPath: []string{"whip"}, Title: "Push this environment's own AI session with the pacing nudge, on demand", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: false},
	// Lease verbs are wrapper and agent busy signals.
	"activity_lease_list":    {Family: "activity", CLIPath: nil, Title: "List activity leases held on an environment", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false, AgentFacing: true},
	"activity_lease_take":    {Family: "activity", CLIPath: nil, Title: "Take an activity lease, deferring auto-stop", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: false, AgentFacing: true},
	"activity_lease_release": {Family: "activity", CLIPath: nil, Title: "Release an activity lease", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: false, AgentFacing: true},
	"ai_sessions":            {Family: "activity", CLIPath: nil, Title: "Report structured AI-session status (busy/idle/awaiting-input/exited/oom-killed)", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false, AgentFacing: true},
	"outputs_list":           {Family: "outputs", CLIPath: []string{"outputs", "list"}, Title: "List an environment's build outputs", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	"outputs_download":       {Family: "outputs", CLIPath: []string{"outputs", "download"}, Title: "Download a build output", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	// No desktop Builds view yet; revisit AgentFacing once it exists.
	"build_profile":    {Family: "build", CLIPath: []string{"build", "profile"}, Title: "Show where a past build spent its time, CPU, and I/O", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false, AgentFacing: true},
	"inputs_upload":    {Family: "inputs", CLIPath: []string{"inputs", "upload"}, Title: "Upload a file into an environment", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: false},
	"sshd_sync":        {Family: "sshd", CLIPath: []string{"sshd", "sync"}, Title: "Sync the working tree over SSH", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: false},
	"contribute_clone": {Family: "contribute", CLIPath: []string{"contribute", "clone"}, Title: "Clone the erun source into the environment", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"version":          {Family: "", CLIPath: []string{"version"}, Title: "Report erun build metadata", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	// OpenWorld: controlPlanes queries control planes, consoles, and the registry.
	"list":        {Family: "", CLIPath: []string{"list"}, Title: "List tenants, environments, and the effective target", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"environment": {Family: "", CLIPath: nil, Title: "Report the environment read model: list-style summary, resolved lifecycle state, idle status, cloud-context config, and doctor health", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false, AgentFacing: true},
	"init":        {Family: "", CLIPath: []string{"init"}, Title: "Initialise an environment", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: false},
	"build":       {Family: "", CLIPath: []string{"build"}, Title: "Build the environment's images", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"push":        {Family: "", CLIPath: []string{"push"}, Title: "Push built images to the registry", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"deploy":      {Family: "", CLIPath: []string{"deploy"}, Title: "Deploy a version into an environment", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"publish":     {Family: "", CLIPath: []string{"publish"}, Title: "Publish charts to the registry", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"upgrade":     {Family: "", CLIPath: []string{"upgrade"}, Title: "Upgrade opted-in environments to the latest version", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"release":     {Family: "", CLIPath: []string{"release"}, Title: "Cut a release and publish its artefacts", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"pin":         {Family: "", CLIPath: []string{"pin"}, Title: "Pin an environment to a version", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: false},
	// The desktop's Ports tab calls services as ListEnvironmentServices, which the surface gate matches.
	"services":  {Family: "", CLIPath: []string{"services"}, Title: "List the Services an environment runs, and which are already exposed", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	"expose":    {Family: "", CLIPath: []string{"expose"}, Title: "Publish a service through the platform edge", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"unexpose":  {Family: "", CLIPath: []string{"unexpose"}, Title: "Withdraw a published service", ReadOnly: false, Destructive: true, Idempotent: true, OpenWorld: true},
	"terraform": {Family: "", CLIPath: nil, Title: "Run the environment's Terraform root", ReadOnly: false, Destructive: true, Idempotent: false, OpenWorld: true},
	// Temporarily agent-facing until a desktop e2e surface exists.
	"e2e": {Family: "", CLIPath: []string{"e2e"}, Title: "Run the project's e2e suites against a deployed environment", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true, AgentFacing: true},
	// job_start is a removed-tool stub (see mcpRemovedTools).
	"job_start": {Family: "exec", CLIPath: nil, Title: "Removed: use exec_raw or exec_agent", ReadOnly: true, Destructive: false, Idempotent: true, OpenWorld: false, AgentFacing: true},
	"doctor":    {Family: "", CLIPath: []string{"doctor"}, Title: "Diagnose an environment", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"observe":   {Family: "", CLIPath: []string{"observe"}, Title: "Observe an environment's live state", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	"usage":     {Family: "", CLIPath: []string{"usage"}, Title: "Report an environment's live resource usage", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	"resize":    {Family: "", CLIPath: []string{"resize"}, Title: "Resize the runtime pod's CPU/memory limits", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"delete":    {Family: "", CLIPath: []string{"delete"}, Title: "Delete an environment and its namespace", ReadOnly: false, Destructive: true, Idempotent: true, OpenWorld: true},
}

// MCPToolDescriptorFor returns a tool's descriptor; callers must treat a missing one as an error.
func MCPToolDescriptorFor(tool string) (MCPToolDescriptor, bool) {
	descriptor, ok := mcpToolDescriptors[MCPToolCurrentName(tool)]
	return descriptor, ok
}

// MCPToolIsMCPOnly reports whether a tool has no erun command behind it (_meta.mcpOnly).
func MCPToolIsMCPOnly(tool string) bool {
	descriptor, ok := MCPToolDescriptorFor(tool)
	return ok && descriptor.CLIPath == nil
}

// MCPToolIsAgentFacing reports whether a tool is exempt from needing a desktop entry point; unknown tools are not.
func MCPToolIsAgentFacing(tool string) bool {
	descriptor, ok := MCPToolDescriptorFor(tool)
	return ok && descriptor.AgentFacing
}

// MCPToolNames returns every tool the descriptor table covers.
func MCPToolNames() []string {
	names := make([]string, 0, len(mcpToolDescriptors))
	for name := range mcpToolDescriptors {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// mcpToolRenames maps retired names to current ones (name = CLI path with "_" for spaces). Old
// names stay callable for one release so pinned clients survive an upgrade.
var mcpToolRenames = map[string]string{
	"diff":           "exec_diff",
	"raw":            "exec_raw",
	"write":          "exec_write",
	"commit":         "exec_commit",
	"workspace_sync": "sshd_sync",
	"job_attach":     "exec_job_attach",
	"job_status":     "exec_job_status",
	"job_await":      "exec_job_await",
	"job_output":     "exec_job_output",
	"job_cancel":     "exec_job_cancel",
}

// MCPToolRenames returns the retired-to-current mapping.
func MCPToolRenames() map[string]string {
	renames := make(map[string]string, len(mcpToolRenames))
	for old, current := range mcpToolRenames {
		renames[old] = current
	}
	return renames
}

// MCPToolCurrentName resolves a retired name so authorization and auditing key off one name.
func MCPToolCurrentName(tool string) string {
	trimmed := strings.TrimSpace(tool)
	if current, ok := mcpToolRenames[trimmed]; ok {
		return current
	}
	return trimmed
}

// mcpRemovedTools maps removed tools to replacement guidance. Unlike a rename, a removal can
// split across tools with incompatible schemas, so it gets a stub that fails with guidance.
var mcpRemovedTools = map[string]string{
	"job_start": "exec_raw with wait:false for a plain command, or exec_agent for an AI tool",
}

// MCPRemovedTools returns the retired-to-guidance mapping.
func MCPRemovedTools() map[string]string {
	removed := make(map[string]string, len(mcpRemovedTools))
	for name, guidance := range mcpRemovedTools {
		removed[name] = guidance
	}
	return removed
}
