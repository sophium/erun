package toolio

// Platform tools reuse the CLI's platform.RunPlatform* functions; preview maps to
// core.Context.DryRun, so the HTTP call is traced but not sent.

type PlatformAliasInput struct {
	Alias     string `json:"alias,omitempty" jsonschema:"erun platform cloud alias to target; defaults to the sole configured erun-type alias"`
	Preview   bool   `json:"preview,omitempty" jsonschema:"when true, trace the resolved call without sending it"`
	Verbosity int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}
