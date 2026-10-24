package shared

// platform.go exposes `erun platform`'s operations as MCP tools over the same
// shared eruncommon.RunPlatform* functions the CLI drives (erun-cli/cmd/platform/platform.go),
// so the two transports can never disagree about what a call does. Preview
// (this module's non-interactive analogue of --dry-run) reuses the exact same
// trace: every tool runs its RunPlatform* call with Preview forwarded straight
// into eruncommon.Context.DryRun, so the resolved HTTP call is traced and no
// network request is sent, with no separately hand-maintained plan text to
// drift from the real path.

type PlatformAliasInput struct {
	Alias     string `json:"alias,omitempty" jsonschema:"erun platform cloud alias to target; defaults to the sole configured erun-type alias"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, trace the resolved call without sending it"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}
