package eruncommon

import (
	"sort"
	"strings"
)

// A tool's family and its blast radius are part of the wire contract, not
// documentation. Before this table every erun tool went out on the MCP spec
// defaults -- readOnlyHint false, destructiveHint true, openWorldHint true --
// because the SDK models the latter two as *bool and treats nil as true. So
// `version` and `list` were advertised as destructive open-world tools and
// `exec_raw` was indistinguishable from them, which means a client wanting to
// gate on destructiveHint had to gate everything or nothing (#1186).
//
// This lives in erun-common for the same reason mcpReadOnlyTools does: the edge
// and every other transport must read one mapping rather than each keeping its
// own, which is the drift that produced #1148 and #1161.

// MCPToolDescriptor is the wire-visible metadata for one tool.
type MCPToolDescriptor struct {
	// Family groups the tool on the surface; empty for a top-level tool.
	// Exposed as _meta.family so a client groups by reading a field instead of
	// splitting a name on "_" -- which does not work anyway, since
	// activity_lease_* and idle_stop_* carry two-segment prefixes.
	Family string
	// CLIPath is the erun command behind the tool, as path segments. Nil when
	// there is no command: eleven tools are wire-only. An absent cliPath is more
	// informative than a fabricated one -- a client learns the tool is wire-only
	// rather than being handed a command that does not exist.
	CLIPath []string
	// Title is the human display name, so a UI need not render
	// "platform_context_create" verbatim.
	Title string
	// ReadOnly means the tool does not modify its environment. This is a
	// SEMANTIC claim, deliberately NOT derived from mcpReadOnlyTools: that table
	// is an authorization allowlist and is stricter on purpose (anything absent
	// requires admin), so platform_env_list is read-only in meaning while still
	// requiring admin to call. Deriving one from the other would mark every
	// platform read as mutating. TestMCPReadOnlyToolsAreSemanticallyReadOnly
	// enforces the one direction that must hold.
	ReadOnly bool
	// Destructive means the tool may destroy or overwrite state rather than only
	// adding. Meaningful only when ReadOnly is false.
	Destructive bool
	// Idempotent means calling it again with the same arguments changes nothing
	// further.
	Idempotent bool
	// OpenWorld means the tool reaches systems outside this environment: a
	// registry, a cloud API, the control plane, or arbitrary argv.
	OpenWorld bool
}

// mcpToolDescriptors covers every registered tool. A tool absent from this map
// is a programming error, and the transport refuses to register it rather than
// letting it ship on the spec defaults -- which is exactly how the surface
// reached the state #1186 describes.
var mcpToolDescriptors = map[string]MCPToolDescriptor{
	"exec_diff":   {Family: "exec", CLIPath: []string{"exec", "diff"}, Title: "Show repository diff", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	"exec_raw":    {Family: "exec", CLIPath: []string{"exec", "raw"}, Title: "Run a raw command in the environment", ReadOnly: false, Destructive: true, Idempotent: false, OpenWorld: true},
	"exec_write":  {Family: "exec", CLIPath: []string{"exec", "write"}, Title: "Write a file in the working tree", ReadOnly: false, Destructive: true, Idempotent: false, OpenWorld: false},
	"exec_commit": {Family: "exec", CLIPath: []string{"exec", "commit"}, Title: "Commit working-tree changes", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: false},
	"exec_push":   {Family: "exec", CLIPath: []string{"exec", "push"}, Title: "Push a working-tree branch to a remote", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	// exec_agent has no CLI path: the CLI already offers this exact
	// capability as `erun exec job start --agent`, one command covering both
	// modes via a flag. MCP cannot do the same because each tool has one
	// fixed schema, which is the whole reason exec_agent exists as a
	// separate tool from exec_raw in the first place -- adding a CLI command
	// that only re-exposes an existing flag would be a wrapper, not new
	// capability.
	"exec_agent":                    {Family: "exec", CLIPath: nil, Title: "Run an AI tool as a detached job", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"exec_job_attach":               {Family: "exec", CLIPath: []string{"exec", "job", "attach"}, Title: "Attach to a running job", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: false},
	"exec_job_status":               {Family: "exec", CLIPath: []string{"exec", "job", "status"}, Title: "Report a job's state and outcome", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	"exec_job_await":                {Family: "exec", CLIPath: []string{"exec", "job", "await"}, Title: "Wait for a job to reach a terminal state", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	"exec_job_output":               {Family: "exec", CLIPath: []string{"exec", "job", "output"}, Title: "Read a job's captured output", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	"exec_job_cancel":               {Family: "exec", CLIPath: []string{"exec", "job", "cancel"}, Title: "Cancel a running job", ReadOnly: false, Destructive: true, Idempotent: true, OpenWorld: false},
	"cloud_list":                    {Family: "cloud", CLIPath: nil, Title: "List configured cloud provider aliases", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	"cloud_init_aws":                {Family: "cloud", CLIPath: []string{"cloud", "init", "aws"}, Title: "Register an AWS provider alias", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"cloud_init_cloudflare":         {Family: "cloud", CLIPath: []string{"cloud", "init", "cloudflare"}, Title: "Register a Cloudflare provider alias", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"cloud_init_erun":               {Family: "cloud", CLIPath: []string{"cloud", "init", "erun"}, Title: "Register an erun platform alias", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"cloud_login":                   {Family: "cloud", CLIPath: []string{"cloud", "login"}, Title: "Sign in to a cloud provider", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"cloud_oidc":                    {Family: "cloud", CLIPath: []string{"cloud", "oidc"}, Title: "Configure OIDC for a provider alias", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"cloud_set":                     {Family: "cloud", CLIPath: []string{"cloud", "set"}, Title: "Set an environment's cloud alias", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: false},
	"cloud_inject_aws_credentials":  {Family: "cloud", CLIPath: nil, Title: "Inject AWS credentials into an environment", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: false},
	"cloud_clear_aws_credentials":   {Family: "cloud", CLIPath: nil, Title: "Clear injected AWS credentials", ReadOnly: false, Destructive: true, Idempotent: true, OpenWorld: false},
	"context_list":                  {Family: "context", CLIPath: []string{"context", "list"}, Title: "List cloud contexts", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	"context_init":                  {Family: "context", CLIPath: []string{"context", "init"}, Title: "Bootstrap a new cloud context", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"context_start":                 {Family: "context", CLIPath: []string{"context", "start"}, Title: "Start a cloud context", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"context_stop":                  {Family: "context", CLIPath: []string{"context", "stop"}, Title: "Stop a cloud context", ReadOnly: false, Destructive: true, Idempotent: true, OpenWorld: true},
	"platform_whoami":               {Family: "platform", CLIPath: []string{"platform", "whoami"}, Title: "Report the authenticated platform identity", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_tenant_list":          {Family: "platform", CLIPath: []string{"platform", "tenant", "list"}, Title: "List platform tenants", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_tenant_create":        {Family: "platform", CLIPath: []string{"platform", "tenant", "create"}, Title: "Create a platform tenant", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_user_list":            {Family: "platform", CLIPath: []string{"platform", "user", "list"}, Title: "List platform users", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_user_enroll":          {Family: "platform", CLIPath: []string{"platform", "user", "enroll"}, Title: "Enrol a user into a tenant", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_env_list":             {Family: "platform", CLIPath: []string{"platform", "env", "list"}, Title: "List hosted environments", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_env_get":              {Family: "platform", CLIPath: []string{"platform", "env", "get"}, Title: "Get one hosted environment", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_env_register":         {Family: "platform", CLIPath: []string{"platform", "env", "register"}, Title: "Register a hosted environment", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_env_deploy":           {Family: "platform", CLIPath: []string{"platform", "env", "deploy"}, Title: "Deploy a hosted environment", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_env_stop":             {Family: "platform", CLIPath: []string{"platform", "env", "stop"}, Title: "Stop a hosted environment", ReadOnly: false, Destructive: true, Idempotent: true, OpenWorld: true},
	"platform_env_delete":           {Family: "platform", CLIPath: []string{"platform", "env", "delete"}, Title: "Delete a hosted environment and its namespace", ReadOnly: false, Destructive: true, Idempotent: true, OpenWorld: true},
	"platform_context_list":         {Family: "platform", CLIPath: []string{"platform", "context", "list"}, Title: "List platform cloud contexts", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_context_get":          {Family: "platform", CLIPath: []string{"platform", "context", "get"}, Title: "Get one platform cloud context", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_context_create":       {Family: "platform", CLIPath: []string{"platform", "context", "create"}, Title: "Create a platform cloud context", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"platform_provision":            {Family: "platform", CLIPath: []string{"platform", "provision"}, Title: "Preview the plan for provisioning a hosted environment", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"review_list":                   {Family: "review", CLIPath: []string{"review", "list"}, Title: "List reviews", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"review_show":                   {Family: "review", CLIPath: []string{"review", "show"}, Title: "Show a review, its comments, and its builds", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"review_create":                 {Family: "review", CLIPath: []string{"review", "create"}, Title: "Open a review", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"review_comment":                {Family: "review", CLIPath: []string{"review", "comment"}, Title: "Comment on a review", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"review_close":                  {Family: "review", CLIPath: []string{"review", "close"}, Title: "Close a review", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"review_resolve":                {Family: "review", CLIPath: []string{"review", "resolve"}, Title: "Resolve a comment thread", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"review_unresolve":              {Family: "review", CLIPath: []string{"review", "unresolve"}, Title: "Reopen a comment thread", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"review_queue_list":             {Family: "review", CLIPath: []string{"review", "queue", "list"}, Title: "List a merge queue", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: true},
	"review_queue_advance":          {Family: "review", CLIPath: []string{"review", "queue", "advance"}, Title: "Advance a merge queue", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"review_queue_override-advance": {Family: "review", CLIPath: []string{"review", "queue", "override-advance"}, Title: "Override the unresolved-thread gate and advance a merge queue", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"idle":                          {Family: "idle", CLIPath: []string{"idle"}, Title: "Report an environment's idle and auto-stop state", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	"idle_stop_history":             {Family: "idle", CLIPath: nil, Title: "List recorded auto-stop decisions", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	"idle_stop_record":              {Family: "idle", CLIPath: nil, Title: "Record an auto-stop decision", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: false},
	"idle_stop_cancel":              {Family: "idle", CLIPath: nil, Title: "Cancel a pending auto-stop", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: false},
	"activity_lease_list":           {Family: "activity", CLIPath: nil, Title: "List activity leases held on an environment", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	"activity_lease_take":           {Family: "activity", CLIPath: nil, Title: "Take an activity lease, deferring auto-stop", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: false},
	"activity_lease_release":        {Family: "activity", CLIPath: nil, Title: "Release an activity lease", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: false},
	"outputs_list":                  {Family: "outputs", CLIPath: []string{"outputs", "list"}, Title: "List an environment's build outputs", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	"outputs_download":              {Family: "outputs", CLIPath: []string{"outputs", "download"}, Title: "Download a build output", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	"inputs_upload":                 {Family: "inputs", CLIPath: []string{"inputs", "upload"}, Title: "Upload a file into an environment", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: false},
	"sshd_sync":                     {Family: "sshd", CLIPath: []string{"sshd", "sync"}, Title: "Sync the working tree over SSH", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: false},
	"contribute_clone":              {Family: "contribute", CLIPath: []string{"contribute", "clone"}, Title: "Clone the erun source into the environment", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"version":                       {Family: "", CLIPath: []string{"version"}, Title: "Report erun build metadata", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	"list":                          {Family: "", CLIPath: []string{"list"}, Title: "List tenants, environments, and the effective target", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	"init":                          {Family: "", CLIPath: []string{"init"}, Title: "Initialise an environment", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: false},
	"build":                         {Family: "", CLIPath: []string{"build"}, Title: "Build the environment's images", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"push":                          {Family: "", CLIPath: []string{"push"}, Title: "Push built images to the registry", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"deploy":                        {Family: "", CLIPath: []string{"deploy"}, Title: "Deploy a version into an environment", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"publish":                       {Family: "", CLIPath: []string{"publish"}, Title: "Publish charts to the registry", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"upgrade":                       {Family: "", CLIPath: []string{"upgrade"}, Title: "Upgrade opted-in environments to the latest version", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"release":                       {Family: "", CLIPath: []string{"release"}, Title: "Cut a release and publish its artefacts", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"pin":                           {Family: "", CLIPath: []string{"pin"}, Title: "Pin an environment to a version", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: false},
	"expose":                        {Family: "", CLIPath: []string{"expose"}, Title: "Publish a service through the platform edge", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"unexpose":                      {Family: "", CLIPath: []string{"unexpose"}, Title: "Withdraw a published service", ReadOnly: false, Destructive: true, Idempotent: true, OpenWorld: true},
	"terraform":                     {Family: "", CLIPath: nil, Title: "Run the environment's Terraform root", ReadOnly: false, Destructive: true, Idempotent: false, OpenWorld: true},
	"doctor":                        {Family: "", CLIPath: []string{"doctor"}, Title: "Diagnose an environment", ReadOnly: false, Destructive: false, Idempotent: false, OpenWorld: true},
	"observe":                       {Family: "", CLIPath: []string{"observe"}, Title: "Observe an environment's live state", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	"usage":                         {Family: "", CLIPath: []string{"usage"}, Title: "Report an environment's live resource usage", ReadOnly: true, Destructive: false, Idempotent: false, OpenWorld: false},
	"resize":                        {Family: "", CLIPath: []string{"resize"}, Title: "Resize the runtime pod's CPU/memory limits", ReadOnly: false, Destructive: false, Idempotent: true, OpenWorld: true},
	"delete":                        {Family: "", CLIPath: []string{"delete"}, Title: "Delete an environment and its namespace", ReadOnly: false, Destructive: true, Idempotent: true, OpenWorld: true},
}

// MCPToolDescriptorFor returns a tool's descriptor. The second result is false
// for an unknown tool, which a caller must treat as an error rather than
// substituting a default.
func MCPToolDescriptorFor(tool string) (MCPToolDescriptor, bool) {
	descriptor, ok := mcpToolDescriptors[MCPToolCurrentName(tool)]
	return descriptor, ok
}

// MCPToolIsMCPOnly reports whether a tool has no erun command behind it.
// Eleven do: the activity_lease_*, idle_stop_* and three cloud tools are
// wire-level primitives the CLI expresses differently for a human,
// `terraform` is a command group rather than a leaf, and `exec_agent`'s
// capability is already covered by an existing CLI flag (`erun exec job
// start --agent`) rather than a dedicated command. Surfaced as _meta.mcpOnly.
func MCPToolIsMCPOnly(tool string) bool {
	descriptor, ok := MCPToolDescriptorFor(tool)
	return ok && descriptor.CLIPath == nil
}

// MCPToolNames returns every tool the descriptor table covers, so a test can
// assert the table and the registered surface agree.
func MCPToolNames() []string {
	names := make([]string, 0, len(mcpToolDescriptors))
	for name := range mcpToolDescriptors {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// mcpToolRenames maps a retired tool name to its replacement. The rule across
// the surface is that a tool's name equals its CLI path with "_" for spaces.
// The first five broke it because `erun exec` was the only command group
// whose members dropped their prefix, and it is the group whose members
// differ most in blast radius -- exec_diff only reads while exec_raw runs
// arbitrary argv. The job_* five are #1246's rename of the same shape:
// `job` becomes a sub-family of `exec` (#1186), so its CLI path moves from
// `erun job <verb>` to `erun exec job <verb>` and the tool names move with
// it. The old names stay callable for one release so an upgrade does not
// break a pinned client, then go.
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

// MCPToolRenames returns the retired-to-current mapping, so a transport can
// register a deprecated alias beside each renamed tool and a test can assert
// every alias resolves.
func MCPToolRenames() map[string]string {
	renames := make(map[string]string, len(mcpToolRenames))
	for old, current := range mcpToolRenames {
		renames[old] = current
	}
	return renames
}

// MCPToolCurrentName resolves a possibly-retired name to the current one, so
// authorization and auditing key off a single name whichever the caller used.
func MCPToolCurrentName(tool string) string {
	trimmed := strings.TrimSpace(tool)
	if current, ok := mcpToolRenames[trimmed]; ok {
		return current
	}
	return trimmed
}
